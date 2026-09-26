// :feature-auth — passkey/Credential Manager, fluxo de login.
plugins {
    alias(libs.plugins.android.library)
    alias(libs.plugins.kotlin.compose)
}

android {
    namespace = "com.vpsmanager.feature.auth"
    compileSdk = libs.versions.compileSdk.get().toInt()

    defaultConfig {
        minSdk = libs.versions.minSdk.get().toInt()
    }

    testOptions {
        unitTests.isIncludeAndroidResources = true
    }
}

dependencies {
    // Bloqueio do app (TelaDeSeguranca / BloqueioDoApp).
    implementation(libs.androidx.biometric)
    implementation(libs.androidx.datastore.preferences)
    implementation(project(":core"))
    implementation(project(":data"))
    // State colours (ok/warning/critical) for the Home panel — Material 3 has
    // no semantic role for "warning"; see StatusColors.kt.
    implementation(project(":design-system"))

    implementation(platform(libs.compose.bom))
    implementation(libs.compose.runtime)
    implementation(libs.compose.ui)
    implementation(libs.compose.foundation)
    implementation(libs.compose.material3)
    // The "core" set (823 KB): Warning/Refresh/KeyboardArrow* for the Home
    // panel. The extended package weighs 35.7 MB and this build does not minify.
    implementation(libs.compose.material.icons.core)
    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.lifecycle.runtime.compose)
    implementation(libs.kotlinx.coroutines.core)
    // Runtime CAMERA permission request (rememberLauncherForActivityResult) in
    // PairingScanScreen.
    implementation(libs.androidx.activity.compose)
    // QR scanning for the pairing flow: CameraX preview/analysis + zxing-core
    // pure-JVM decode, chosen over ML Kit to avoid a Play Services dependency
    // (see gradle/libs.versions.toml).
    implementation(libs.androidx.camera.core)
    implementation(libs.androidx.camera.camera2)
    implementation(libs.androidx.camera.lifecycle)
    implementation(libs.androidx.camera.view)
    implementation(libs.zxing.core)

    testImplementation(libs.junit)
    testImplementation(libs.robolectric)
    testImplementation(libs.androidx.test.core)
    testImplementation(libs.kotlinx.coroutines.test)
    testImplementation(platform(libs.compose.bom))
    testImplementation(libs.compose.ui.test.junit4)
    debugImplementation(libs.compose.ui.test.manifest)
}
