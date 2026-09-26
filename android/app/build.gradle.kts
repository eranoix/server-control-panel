// :app — the only Android application module. Navigable shell (theme, edge-to-
// edge, NavHost) and the real vertical slice of Home; every other area is still
// a placeholder. The only module with targetSdk (AGP 9.3 does not expose
// targetSdk in com.android.library — see android/data/build.gradle.kts).
import org.gradle.api.DefaultTask
import org.gradle.api.GradleException
import org.gradle.api.file.RegularFileProperty
import org.gradle.api.provider.Property
import org.gradle.api.tasks.Input
import org.gradle.api.tasks.InputFile
import org.gradle.api.tasks.TaskAction
import org.gradle.kotlin.dsl.register

plugins {
    alias(libs.plugins.android.application)
    alias(libs.plugins.kotlin.compose)
}

// applicationId is the DISTRIBUTION identity (assetlinks.json, F-Droid,
// Developer Verification) — independent of the Kotlin/R-class namespace below.
// Single literal in android/gradle.properties (vpsmanager.applicationId);
// verifyApplicationIdMatchesDocs, registered further down, fails the build if
// this value diverges from the one documented in docs/android-signing-keystore.md
// section 2 (see section 7 of that document on why reading
// data/config.json at build time is not viable: it is a runtime file, outside git, that
// does not exist in the CI checkout).
val applicationIdFromProperties = requireNotNull(
    project.findProperty("vpsmanager.applicationId") as String?
) {
    "vpsmanager.applicationId ausente em android/gradle.properties — " +
        "ver docs/android-signing-keystore.md Seção 7"
}

android {
    namespace = "com.vpsmanager.app"
    compileSdk = libs.versions.compileSdk.get().toInt()

    defaultConfig {
        applicationId = applicationIdFromProperties
        minSdk = libs.versions.minSdk.get().toInt()
        targetSdk = libs.versions.targetSdk.get().toInt()
        // versionCode/versionName are read from the Gradle properties passed
        // by the CI android-release job (github.run_number / android-v* tag
        // — see .github/workflows/ci.yml and docs/android-release-pipeline.md);
        // without them (local build) they fall back to the development default.
        versionCode = (project.findProperty("versionCode") as String?)?.toIntOrNull() ?: 1
        versionName = project.findProperty("versionName") as String? ?: "0.1.0"

        // ABIs — arm64-v8a + x86_64 (real device + emulator).
        // Without this filter the APK carries all FOUR that stream-webrtc-android
        // packages, including x86 (11.8 MB) and armeabi-v7a (6.3 MB) that no
        // target of this project uses — 18 MB of dead weight in a download that is already
        // large. `vpsmanager.abi` allows narrowing further for a single-device
        // build (e.g. -Pvpsmanager.abi=arm64-v8a saves another 20 MB).
        ndk {
            val soAbi = project.findProperty("vpsmanager.abi") as String?
            abiFilters += soAbi?.split(",")?.map { it.trim() } ?: listOf("arm64-v8a", "x86_64")
        }

        // Default server seeded on first boot (see MainActivity). Empty by
        // default: without the property the app falls back to the setup screen as before. It is not
        // a secret — it is the panel's public origin.
        buildConfigField(
            "String",
            "DEFAULT_SERVER_URL",
            "\"${project.findProperty("vpsmanager.defaultServerUrl") ?: ""}\"",
        )
    }

    // Only consumer: AppNavHost gates the debug/sdui-preview destination on
    // BuildConfig.DEBUG so it never reaches the release nav graph.
    buildFeatures {
        buildConfig = true
    }

    testOptions {
        unitTests.isIncludeAndroidResources = true
    }

    buildTypes {
        release {
            // Minification/obfuscation (R8) and removal of unreferenced resources.
            //
            // The .dex files are by far the largest block of this APK — larger than the
            // native .so put together. The app's owner downloads the APK on his phone, over
            // a bad connection: every MB is a real cost to him, not a vanity
            // number.
            //
            // This stayed off until now for a legitimate reason: a wrong keep
            // rule does not break the build, it breaks at RUNTIME (the
            // terminal stops opening, the login stops parsing), and there
            // was no way to run the app and find out. With the Lab emulator
            // there now is — the rules in
            // proguard-rules.pro were derived from this project's real dependencies
            // and the minified APK was exercised on the device before
            // this flag was committed.
            isMinifyEnabled = true
            isShrinkResources = true

            // `proguard-android-optimize.txt` (and not `proguard-android.txt`):
            // it is the same base set, with R8's optimizations enabled —
            // which is exactly the point of turning this on.
            proguardFiles(
                getDefaultProguardFile("proguard-android-optimize.txt"),
                file("proguard-rules.pro"),
            )
        }
    }

    // DEVELOPMENT signing — strictly opt-in.
    //
    // Without the `vpsmanager.devKeystore` property, the release build comes out
    // UNSIGNED, which is the correct and deliberate behaviour: the release key
    // must never exist on this VPS (see docs/android-signing-keystore.md), so
    // CI compiles without signing and the operator signs offline. A signingConfig
    // enabled by default would erase that guarantee with nobody noticing.
    //
    // With the property, it applies the disposable key documented in
    // docs/android-chave-dev.md and marks versionName with `-devsigned`, so that
    // the APK identifies itself as such in `aapt2 dump badging`, on the about screen and
    // in any F-Droid index — an artifact signed with a dev key must never
    // be mistaken for a publishable release.
    val devKeystorePath = project.findProperty("vpsmanager.devKeystore") as String?
    if (devKeystorePath != null) {
        signingConfigs {
            create("dev") {
                storeFile = file(devKeystorePath)
                storePassword = project.findProperty("vpsmanager.devKeystorePassword") as String?
                    ?: "devkey-nao-secreta"
                keyAlias = "vpsmanager-dev"
                keyPassword = storePassword
            }
        }
        buildTypes {
            release {
                signingConfig = signingConfigs.getByName("dev")
                versionNameSuffix = "-devsigned"
            }
        }
    }
}

dependencies {
    // MainActivity e FragmentActivity (BiometricPrompt exige).
    implementation(libs.androidx.fragment)
    implementation(project(":core"))

    // The home-screen widget. Glance because a widget is ANOTHER
    // process (the launcher) drawing through RemoteViews: without it, this would be XML +
    // RemoteViews by hand, a second UI language to maintain in
    // parallel with the whole rest of the app in Compose.
    implementation(libs.androidx.glance.appwidget)
    implementation(libs.androidx.glance.material3)
    implementation(project(":data"))
    implementation(project(":sdui"))
    implementation(project(":design-system"))
    implementation(project(":feature-terminal"))
    implementation(project(":feature-admin"))
    implementation(project(":feature-auth"))
    implementation(project(":feature-notifications"))
    implementation(project(":feature-videocall"))
    implementation(project(":feature-whatsapp"))
    implementation(project(":feature-files"))
    implementation(project(":feature-jira"))

    implementation(platform(libs.compose.bom))
    implementation(libs.compose.runtime)
    implementation(libs.compose.ui)
    implementation(libs.compose.ui.tooling.preview)
    implementation(libs.compose.foundation)
    implementation(libs.compose.material3)
    // Navigation-drawer icons. The `core` set (823 KB) and not
    // `extended` (35.7 MB) — see the note in gradle/libs.versions.toml about
    // why that still holds even with R8 on. The missing glyphs
    // come from VpsmIcons, in :design-system, with the official Material
    // path data.
    implementation(libs.compose.material.icons.core)
    implementation(libs.androidx.activity.compose)
    implementation(libs.androidx.navigation.compose)
    implementation(libs.androidx.core.ktx)
    // ProcessLifecycleOwner — the single place MobileEventsSocket.start()/stop() are wired,
    // driven by the whole app's foreground/background transitions.
    implementation(libs.androidx.lifecycle.process)
    // UpdateCheckWorker: the periodic check for an update of the app itself.
    // :app declares WorkManager directly (and not through :feature-files,
    // which keeps it as `implementation` on purpose) because the worker lives
    // here — the update belongs to the shell, not to a feature.
    implementation(libs.androidx.work.runtime.ktx)

    // Pure JVM unit tests for the deep-link route resolution/consumption logic —
    // no Android framework classes involved, so plain JUnit is enough (no Robolectric).
    testImplementation(libs.junit)
    // Bootstrap/VpsManagerApplication launch-path tests touch a real Context (crash file
    // persistence, TelecomManager, NotificationManager) — same Robolectric setup :data and
    // :feature-videocall already use for the same reason.
    testImplementation(libs.robolectric)
    testImplementation(libs.androidx.test.core)
    testImplementation(libs.kotlinx.coroutines.test)
    testImplementation(libs.androidx.work.testing)
    testImplementation(platform(libs.compose.bom))
    testImplementation(libs.compose.ui.test.junit4)
    debugImplementation(libs.compose.ui.test.manifest)
}

// Fails the build if the applicationId in gradle.properties diverges from the value
// documented in docs/android-signing-keystore.md section 2. The only anti-drift
// mechanism available while the value cannot be read directly
// from data/config.json (runtime, outside git — see section 7 of that
// document): a "keep in sync" comment blocks nothing, this
// task does.
abstract class VerifyApplicationIdMatchesDocsTask : DefaultTask() {

    @get:Input
    abstract val expectedApplicationId: Property<String>

    @get:InputFile
    abstract val docsFile: RegularFileProperty

    @TaskAction
    fun verify() {
        val file = docsFile.get().asFile
        if (!file.isFile) {
            throw GradleException(
                "verifyApplicationIdMatchesDocs: ${file.path} não encontrado — " +
                    "não é possível confirmar que o applicationId do Gradle bate " +
                    "com a Seção 2 do runbook de assinatura."
            )
        }

        val heading = "## 2. Application ID"
        val afterHeading = file.readText().substringAfter(heading, missingDelimiterValue = "")
        if (afterHeading.isEmpty()) {
            throw GradleException(
                "verifyApplicationIdMatchesDocs: seção '$heading' não encontrada em " +
                    "${file.path} — o runbook mudou de formato, ajuste esta task."
            )
        }

        val documented = Regex("```\\n(.*?)\\n```", RegexOption.DOT_MATCHES_ALL)
            .find(afterHeading)
            ?.groupValues
            ?.get(1)
            ?.trim()

        val expected = expectedApplicationId.get()
        if (documented != expected) {
            throw GradleException(
                "applicationId diverge entre o build e a documentação:\n" +
                    "  gradle.properties (vpsmanager.applicationId): $expected\n" +
                    "  ${file.path} Seção 2: ${documented ?: "<não encontrado>"}\n" +
                    "Os dois precisam bater — atualize um dos dois antes de continuar."
            )
        }
    }
}

val verifyApplicationIdMatchesDocs = tasks.register<VerifyApplicationIdMatchesDocsTask>(
    "verifyApplicationIdMatchesDocs"
) {
    group = "verification"
    description = "Fails the build if applicationId disagrees with docs/android-signing-keystore.md §2"
    expectedApplicationId.set(applicationIdFromProperties)
    docsFile.set(
        rootProject.layout.projectDirectory.dir("..").file("docs/android-signing-keystore.md")
    )
}

tasks.named("preBuild") {
    dependsOn(verifyApplicationIdMatchesDocs)
}
