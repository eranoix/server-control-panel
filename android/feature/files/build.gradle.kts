// :feature-files: remote file browser and editor.
plugins {
    alias(libs.plugins.android.library)
    alias(libs.plugins.kotlin.compose)
}

android {
    namespace = "com.vpsmanager.feature.files"
    compileSdk = libs.versions.compileSdk.get().toInt()

    defaultConfig {
        minSdk = libs.versions.minSdk.get().toInt()
        // LGPL-2.1 (sora-editor): R8 must not obfuscate or strip it, so users can relink a
        // modified copy. Declared here so it merges into :app's R8 config automatically.
        consumerProguardFiles("consumer-rules.pro")
    }

    // sora-editor ships JVM 17 bytecode with inline functions, which Kotlin cannot inline
    // into a lower target, so this module must also target JVM 17.
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    testOptions {
        unitTests.isIncludeAndroidResources = true
    }
}

dependencies {
    implementation(project(":core"))
    implementation(project(":data"))

    implementation(platform(libs.compose.bom))
    implementation(libs.compose.runtime)
    implementation(libs.compose.ui)
    implementation(libs.compose.foundation)
    implementation(libs.compose.material3)
    // Core icon set only (823 KB); the extended set is deliberately excluded.
    implementation(libs.compose.material.icons.core)
    implementation(libs.androidx.activity.compose)
    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.lifecycle.runtime.compose)
    implementation(libs.kotlinx.coroutines.core)

    // WorkManager: transfer engine that survives process death, with progress and cancel.
    implementation(libs.androidx.work.runtime.ktx)

    // sora-editor (LGPL-2.1-or-later): used only from Maven Central, never vendored.
    // Attribution is in OssLicensesScreen (:app).
    implementation(platform(libs.sora.editor.bom))
    implementation(libs.sora.editor)
    implementation(libs.sora.editor.language.textmate)

    testImplementation(libs.junit)
    testImplementation(libs.kotlinx.coroutines.test)
    // Renders the Compose screens under Robolectric, without an emulator.
    testImplementation(libs.robolectric)
    testImplementation(libs.androidx.test.core)
    testImplementation(platform(libs.compose.bom))
    testImplementation(libs.compose.ui.test.junit4)
    debugImplementation(libs.compose.ui.test.manifest)
    testImplementation(libs.androidx.work.testing)
}
