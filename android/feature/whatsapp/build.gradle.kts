// :feature-whatsapp — conversation list and messages, native UI. The transport
// (WhatsAppWsClient) depends on :data only through the pure interfaces in
// com.vpsmanager.data.whatsapp (WhatsAppRepository, WhatsAppWebSocketPort) — it
// never imports okhttp3/retrofit2 directly; the real OkHttp implementation
// lives whole in :data.
plugins {
    alias(libs.plugins.android.library)
    alias(libs.plugins.kotlin.compose)
    alias(libs.plugins.kotlin.serialization)
}

android {
    namespace = "com.vpsmanager.feature.whatsapp"
    compileSdk = libs.versions.compileSdk.get().toInt()

    defaultConfig {
        minSdk = libs.versions.minSdk.get().toInt()
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
    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.lifecycle.runtime.compose)
    implementation(libs.androidx.navigation.compose)
    implementation(libs.kotlinx.coroutines.core)
    implementation(libs.kotlinx.serialization.json)
    implementation(libs.coil.compose)
    // Playback (video/audio) — ExoPlayer + PlayerView. The
    // Range-request-aware OkHttp DataSource.Factory itself is built in
    // :data (com.vpsmanager.data.media.createMediaDataSourceFactory) — this
    // module only ever sees Media3's own DataSource.Factory type, never
    // okhttp3.* directly.
    implementation(libs.media3.exoplayer)
    implementation(libs.media3.ui)
    // FileProvider content:// grant for document opening (DocumentOpener.kt).
    implementation(libs.androidx.core.ktx)
    // rememberLauncherForActivityResult (AttachmentPicker.kt).
    implementation(libs.androidx.activity.compose)

    testImplementation(libs.junit)
    testImplementation(libs.kotlinx.coroutines.test)
    // MediaCacheTest builds a real coil.ImageLoader against a Context;
    // DocumentOpenerTest builds a real FileProvider content:// Uri — both
    // need a shadowed Android environment, same convention as
    // :feature-terminal's Robolectric unit tests.
    testImplementation(libs.robolectric)
    testImplementation(libs.androidx.test.core)
    testImplementation(platform(libs.compose.bom))
    testImplementation(libs.compose.ui.test.junit4)
    debugImplementation(libs.compose.ui.test.manifest)
}
