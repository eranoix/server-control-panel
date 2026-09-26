// :data — Android library. Repositories, generated OpenAPI client, WS clients
// (OkHttp), local cache. The only module authorized to reference
// okhttp3.*/retrofit2.*/the generated client directly.
plugins {
    alias(libs.plugins.android.library)
    alias(libs.plugins.kotlin.serialization)
}

android {
    namespace = "com.vpsmanager.data"
    compileSdk = libs.versions.compileSdk.get().toInt()

    defaultConfig {
        minSdk = libs.versions.minSdk.get().toInt()
    }

    // SduiDataClient inlines ApiClient.request() from :data:mobile-api-client,
    // a Kotlin/JVM module built against JVM toolchain 17 (its own
    // build.gradle.kts) -- Kotlin cannot inline JVM-17 bytecode into a module
    // compiling at a lower target, so :data must compile at the same target.
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    testOptions {
        unitTests.isIncludeAndroidResources = true
    }
}

dependencies {
    // Device security preferences (PreferenciasDeSeguranca).
    implementation(libs.androidx.datastore.preferences)
    implementation(project(":core"))
    implementation(project(":data:mobile-api-client"))
    // com.vpsmanager.data.update rebuilds the APK with :patch-engine's
    // hpatchz. The boundary is deliberate: :patch-engine does not download and does not
    // install (a file goes in, a file comes out, with the SHA-256 checked), and it is this
    // module that has network, disk and coroutines to give it context. It is also
    // from here that ApkPatcher.sha256Of comes, used to hash the installed APK
    // — the same implementation on both sides of the comparison, by construction.
    implementation(project(":patch-engine"))
    // SduiDataRepository decodes the raw JSON body an SDUI data-source
    // endpoint returns; kotlinx-serialization-json is already an `api`
    // dependency of :data:mobile-api-client (transitively on the classpath),
    // declared explicitly here because this module's own source imports it
    // directly.
    implementation(libs.kotlinx.serialization.json)
    // WorkManager: the offline write queue has to survive closing the
    // app and rebooting the device, and have a built-in network constraint. It is the
    // same engine :feature-files already uses for resumable transfer.
    implementation(libs.androidx.work.runtime.ktx)
    // PasskeyClient's suspend register()/login() drive Credential Manager
    // ceremonies directly — the plain `credentials` artifact suffices
    // because minSdk 34 already carries the framework's own provider (see
    // gradle/libs.versions.toml).
    implementation(libs.androidx.credentials)
    // ServerConfigStore persists the configured server address via
    // EncryptedSharedPreferences (Keystore-backed) — see
    // gradle/libs.versions.toml.
    implementation(libs.androidx.security.crypto)
    // com.vpsmanager.data.media builds the authenticated coil.ImageLoader
    // (thumbnails for WhatsApp images/videos) and the Media3 DataSource.Factory
    // (streaming playback) entirely in this module -- the only one allowed to
    // construct an okhttp3.Call.Factory. coil-video adds
    // VideoFrameDecoder for video thumbnails; media3-datasource-okhttp lets
    // ExoPlayer's Range requests go through the same OkHttpClient/connection
    // pool as every other BFF call instead of opening a second HTTP stack.
    implementation(libs.coil.video)
    implementation(libs.media3.datasource.okhttp)
    // WebRtcSessionManager (com.vpsmanager.data.videocall) owns the
    // PeerConnectionFactory/track lifecycle — the only module allowed to
    // reference org.webrtc.* directly, same architectural boundary as
    // com.vpsmanager.data.media's Call.Factory. -ktx adds the coroutine/Flow
    // wrappers this module's own suspend API is built on; -compose is
    // intentionally NOT declared here (the Compose layer owns that dependency).
    implementation(libs.stream.webrtc.android)
    implementation(libs.stream.webrtc.android.ktx)

    testImplementation(libs.junit)
    testImplementation(libs.kotlinx.coroutines.test)
    testImplementation(libs.okhttp.mockwebserver)
    // DeviceIdProviderTest exercises Settings.Secure/SharedPreferences against a real
    // (shadowed) Android Context — mirrors :feature-terminal's Robolectric test config.
    testImplementation(libs.robolectric)
    testImplementation(libs.androidx.test.core)
}
