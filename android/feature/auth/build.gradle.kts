// Login flow: passkeys via Credential Manager.
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
    // App lock (SecurityScreen / AppLock).
    implementation(libs.androidx.biometric)
    implementation(libs.androidx.datastore.preferences)
    implementation(project(":core"))
    implementation(project(":data"))
    // Status colors for Home; Material 3 has no "warning" role (see StatusColors.kt).
    implementation(project(":design-system"))

    implementation(platform(libs.compose.bom))
    implementation(libs.compose.runtime)
    implementation(libs.compose.ui)
    implementation(libs.compose.foundation)
    implementation(libs.compose.material3)
    // Core icon set only (823 KB); the extended set is 35.7 MB.
    implementation(libs.compose.material.icons.core)
    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.lifecycle.runtime.compose)
    implementation(libs.kotlinx.coroutines.core)
    // Runtime CAMERA permission request in PairingScanScreen.
    implementation(libs.androidx.activity.compose)
    // Pairing QR scan: CameraX plus pure-JVM zxing, avoiding ML Kit's Play Services dependency.
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
