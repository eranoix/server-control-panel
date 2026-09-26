// :feature-videocall — chamada de video nativa (WebRTC), unica excecao com
// foreground service persistente.
plugins {
    alias(libs.plugins.android.library)
    alias(libs.plugins.kotlin.compose)
}

android {
    namespace = "com.vpsmanager.feature.videocall"
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
    // Decodes SignalingMessage.payload (a raw JsonElement) into JoinResponse/PeerInfo/
    // SdpPayload/IceCandidatePayload once `type` is known.
    implementation(libs.kotlinx.serialization.json)
    // Runtime RECORD_AUDIO+CAMERA permission request (RequestMultiplePermissions) before
    // joining a call.
    implementation(libs.androidx.activity.compose)
    // org.webrtc.* types (VideoTrack, PeerConnection, SessionDescription, IceCandidate) this
    // screen renders/negotiates against — already an `implementation` dependency of `:data`
    // redeclared here because this module's own source imports them directly.
    implementation(libs.stream.webrtc.android)
    implementation(libs.stream.webrtc.android.ktx)
    implementation(libs.stream.webrtc.android.compose)

    testImplementation(libs.junit)
    testImplementation(libs.kotlinx.coroutines.test)
    // PhoneAccountRegistrarTest constructs real android.telecom.* / android.content.ComponentName
    // instances (Android SDK stub jars throw on any real method call without a shadow) — mirrors
    // :data's own Robolectric setup for the same reason (DeviceIdProviderTest).
    testImplementation(libs.robolectric)
    testImplementation(libs.androidx.test.core)
    testImplementation(platform(libs.compose.bom))
    testImplementation(libs.compose.ui.test.junit4)
    debugImplementation(libs.compose.ui.test.manifest)

    // 11-05: BackgroundedCallSurvivesTest/LockScreenAnswerDeclineTest run on real android.telecom
    // and android.app.Service runtime behavior — Robolectric's Connection/Service shadows are not
    // trustworthy enough for the video-call gates, so these are real connectedAndroidTest, not unit
    // tests. androidTest does not automatically inherit main's `implementation(...)` dependencies
    // (verified against :feature-terminal's own androidTest block, which redeclares
    // project(":terminal-engine") the same way), so every type these test files import directly
    // is redeclared here.
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
