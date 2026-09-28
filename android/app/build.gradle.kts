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
        versionCode = (project.findProperty("versionCode") as String?)?.toIntOrNull() ?: 1
        versionName = project.findProperty("versionName") as String? ?: "0.1.0"

        ndk {
            val soAbi = project.findProperty("servercontrolpanel.abi") as String?
            abiFilters += soAbi?.split(",")?.map { it.trim() } ?: listOf("arm64-v8a", "x86_64")
        }

        buildConfigField(
            "String",
            "DEFAULT_SERVER_URL",
            "\"${project.findProperty("servercontrolpanel.defaultServerUrl") ?: ""}\"",
        )
    }

    buildFeatures {
        buildConfig = true
    }

    testOptions {
        unitTests.isIncludeAndroidResources = true
    }

    buildTypes {
        release {
            isMinifyEnabled = true
            isShrinkResources = true

            proguardFiles(
                getDefaultProguardFile("proguard-android-optimize.txt"),
                file("proguard-rules.pro"),
            )
        }
    }

    val devKeystorePath = project.findProperty("servercontrolpanel.devKeystore") as String?
    if (devKeystorePath != null) {
        signingConfigs {
            create("dev") {
                storeFile = file(devKeystorePath)
                storePassword = project.findProperty("servercontrolpanel.devKeystorePassword") as String?
                    ?: "devkey-not-secret"
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
    implementation(libs.androidx.fragment)
    implementation(project(":core"))

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
    implementation(libs.compose.material.icons.core)
    implementation(libs.androidx.activity.compose)
    implementation(libs.androidx.navigation.compose)
    implementation(libs.androidx.core.ktx)
    implementation(libs.androidx.lifecycle.process)
    implementation(libs.androidx.work.runtime.ktx)

    testImplementation(libs.junit)
    testImplementation(libs.robolectric)
    testImplementation(libs.androidx.test.core)
    testImplementation(libs.kotlinx.coroutines.test)
    testImplementation(libs.androidx.work.testing)
    testImplementation(platform(libs.compose.bom))
    testImplementation(libs.compose.ui.test.junit4)
    debugImplementation(libs.compose.ui.test.manifest)
}

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
