// :feature-videocall: native WebRTC video calls, the only module with a persistent foreground service.
plugins {
    alias(libs.plugins.android.library)
    alias(libs.plugins.kotlin.compose)
}

android {
    namespace = "dev.servercontrolpanel.feature.videocall"
    compileSdk = libs.versions.compileSdk.get().toInt()

    defaultConfig {
        minSdk = libs.versions.minSdk.get().toInt()
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
    }

    testOptions {
        unitTests.isIncludeAndroidResources = true
    }
}

dependencies {
    implementation(project(":data"))

    implementation(platform(libs.compose.bom))
    implementation(libs.compose.runtime)
    implementation(libs.compose.ui)
    implementation(libs.compose.foundation)
    implementation(libs.compose.material3)
    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.lifecycle.runtime.compose)
    implementation(libs.kotlinx.coroutines.core)
    // Decodes SignalingMessage.payload into typed payloads.
    implementation(libs.kotlinx.serialization.json)
    // Runtime RECORD_AUDIO and CAMERA request before joining.
    implementation(libs.androidx.activity.compose)
    // Redeclared from :data because this module imports org.webrtc types directly.
    implementation(libs.stream.webrtc.android)
    implementation(libs.stream.webrtc.android.ktx)
    implementation(libs.stream.webrtc.android.compose)

    testImplementation(libs.junit)
    testImplementation(libs.kotlinx.coroutines.test)
    // Robolectric: tests construct real android.telecom and ComponentName objects, which SDK stubs reject.
    testImplementation(libs.robolectric)
    testImplementation(libs.androidx.test.core)
    testImplementation(platform(libs.compose.bom))
    testImplementation(libs.compose.ui.test.junit4)
    debugImplementation(libs.compose.ui.test.manifest)

    // Call tests run on a device because Robolectric's Connection and Service shadows are not
    // trusted. androidTest does not inherit main's implementation deps, so they are redeclared.
    androidTestImplementation(project(":data"))
    androidTestImplementation(libs.junit)
    androidTestImplementation(libs.kotlinx.coroutines.test)
    androidTestImplementation(libs.kotlinx.serialization.json)
    androidTestImplementation(libs.stream.webrtc.android)
    androidTestImplementation(libs.androidx.test.runner)
    androidTestImplementation(libs.androidx.test.ext.junit)
    androidTestImplementation(libs.androidx.test.core)
    androidTestImplementation(libs.androidx.test.rules)
    androidTestImplementation(libs.androidx.test.uiautomator)
}
