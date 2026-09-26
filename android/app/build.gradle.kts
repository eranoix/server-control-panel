// :app, the only Android application module: the navigable shell (theme, edge-to-edge,
// NavHost). The only module with targetSdk (AGP 9.3 does not expose it in
// com.android.library).
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

// applicationId is the distribution identity (assetlinks.json, F-Droid, Developer
// Verification), independent of the namespace below. Its single source is
// android/gradle.properties; verifyApplicationIdMatchesDocs fails the build if it differs
// from docs/android-signing-keystore.md section 2.
val applicationIdFromProperties = requireNotNull(
    project.findProperty("servercontrolpanel.applicationId") as String?
) {
    "servercontrolpanel.applicationId missing from android/gradle.properties, " +
        "see docs/android-signing-keystore.md section 7"
}

android {
    namespace = "dev.servercontrolpanel.app"
    compileSdk = libs.versions.compileSdk.get().toInt()

    defaultConfig {
        applicationId = applicationIdFromProperties
        minSdk = libs.versions.minSdk.get().toInt()
        targetSdk = libs.versions.targetSdk.get().toInt()
        // versionCode/versionName come from the CI android-release job (run number and
        // android-v* tag); local builds fall back to development defaults.
        versionCode = (project.findProperty("versionCode") as String?)?.toIntOrNull() ?: 1
        versionName = project.findProperty("versionName") as String? ?: "0.1.0"

        // arm64-v8a + x86_64 (device + emulator). Without the filter stream-webrtc-android
        // ships x86 and armeabi-v7a too (18 MB unused). `servercontrolpanel.abi` narrows further for
        // a single-device build (-Pservercontrolpanel.abi=arm64-v8a saves another 20 MB).
        ndk {
            val soAbi = project.findProperty("servercontrolpanel.abi") as String?
            abiFilters += soAbi?.split(",")?.map { it.trim() } ?: listOf("arm64-v8a", "x86_64")
        }

        // Default server seeded on first boot (see MainActivity). Empty means the app shows
        // the setup screen. Not a secret: it is the panel's public origin.
        buildConfigField(
            "String",
            "DEFAULT_SERVER_URL",
            "\"${project.findProperty("servercontrolpanel.defaultServerUrl") ?: ""}\"",
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
            // R8 minification and resource shrinking: .dex is the largest part of the APK,
            // which is downloaded over mobile connections. A wrong keep rule breaks at
            // runtime, not at build time, so test the minified APK on a device after
            // changing proguard-rules.pro.
            isMinifyEnabled = true
            isShrinkResources = true

            // The optimize variant enables R8 optimizations on the same base rules.
            proguardFiles(
                getDefaultProguardFile("proguard-android-optimize.txt"),
                file("proguard-rules.pro"),
            )
        }
    }

    // Development signing, strictly opt-in. Without `servercontrolpanel.devKeystore` the release
    // build is unsigned on purpose: the release key must never exist on this VPS, and the
    // operator signs offline. With it, the disposable dev key is used and versionName gets
    // `-devsigned` so the APK can never be mistaken for a publishable release.
    val devKeystorePath = project.findProperty("servercontrolpanel.devKeystore") as String?
    if (devKeystorePath != null) {
        signingConfigs {
            create("dev") {
                storeFile = file(devKeystorePath)
                storePassword = project.findProperty("servercontrolpanel.devKeystorePassword") as String?
                    ?: "devkey-nao-secreta"
                keyAlias = "servercontrolpanel-dev"
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
    // MainActivity is a FragmentActivity (BiometricPrompt requires it).
    implementation(libs.androidx.fragment)
    implementation(project(":core"))

    // Home screen widget. Glance avoids hand-written XML and RemoteViews as a second UI
    // toolkit next to Compose.
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
    // Drawer icons: the `core` set (823 KB), not `extended` (35.7 MB); see
    // gradle/libs.versions.toml. Missing glyphs come from PanelIcons in :design-system.
    implementation(libs.compose.material.icons.core)
    implementation(libs.androidx.activity.compose)
    implementation(libs.androidx.navigation.compose)
    implementation(libs.androidx.core.ktx)
    // ProcessLifecycleOwner: the single place MobileEventsSocket.start()/stop() are wired,
    // driven by the whole app's foreground/background transitions.
    implementation(libs.androidx.lifecycle.process)
    // UpdateCheckWorker (periodic app self-update check) lives here, so :app declares
    // WorkManager directly instead of relying on :feature-files.
    implementation(libs.androidx.work.runtime.ktx)

    // Pure JVM unit tests for the deep-link route resolution/consumption logic;
    // no Android framework classes involved, so plain JUnit is enough (no Robolectric).
    testImplementation(libs.junit)
    // Bootstrap/PanelApplication launch-path tests touch a real Context (crash file
    // persistence, TelecomManager, NotificationManager), so they use Robolectric.
    testImplementation(libs.robolectric)
    testImplementation(libs.androidx.test.core)
    testImplementation(libs.kotlinx.coroutines.test)
    testImplementation(libs.androidx.work.testing)
    testImplementation(platform(libs.compose.bom))
    testImplementation(libs.compose.ui.test.junit4)
    debugImplementation(libs.compose.ui.test.manifest)
}

// Fails the build if the applicationId in gradle.properties differs from
// docs/android-signing-keystore.md section 2. The runtime config.json is not in git, so this
// check is the only drift guard.
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
                "verifyApplicationIdMatchesDocs: ${file.path} not found, " +
                    "cannot confirm that the Gradle applicationId matches " +
                    "section 2 of the signing runbook."
            )
        }

        val heading = "## 2. Application ID"
        val afterHeading = file.readText().substringAfter(heading, missingDelimiterValue = "")
        if (afterHeading.isEmpty()) {
            throw GradleException(
                "verifyApplicationIdMatchesDocs: section '$heading' not found in " +
                    "${file.path}. The runbook format changed, update this task."
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
                "applicationId differs between the build and the docs:\n" +
                    "  gradle.properties (servercontrolpanel.applicationId): $expected\n" +
                    "  ${file.path} section 2: ${documented ?: "<not found>"}\n" +
                    "They must match. Update one of them before continuing."
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
