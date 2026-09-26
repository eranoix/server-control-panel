// :benchmark — an isolated instrumented module (no production code) purely to
// measure Canvas against SurfaceView under the same synthetic load. It depends
// on :feature-terminal (both renderers) and :terminal-engine (the real engine
// that generates the load's CellSnapshots). It is where the rendering verdict
// and its measurement method come from.
plugins {
    alias(libs.plugins.android.library)
    // Without the Compose compiler plugin this module compiles
    // `ComposeView.setContent { ... }` as an ordinary lambda (Function0) and the
    // composable calls do not go through the transformation — on the device that
    // blows up with NoSuchMethodError, because the real signature is Function2
    // (that is where the Composer comes in). It was the reason the two
    // benchmarks had never run: they compiled and failed only at runtime.
    alias(libs.plugins.kotlin.compose)
}

android {
    namespace = "com.vpsmanager.benchmark"
    compileSdk = libs.versions.compileSdk.get().toInt()

    defaultConfig {
        minSdk = libs.versions.minSdk.get().toInt()
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
    }

    // The >=8MB synthetic fixture used by GridThroughputBenchmark lives in
    // androidTest/assets (not main/assets): it is test data, never shipped
    // inside the real app.
    sourceSets {
        getByName("androidTest") {
            assets.srcDirs("src/androidTest/assets")
        }
    }
}

dependencies {
    implementation(libs.androidx.activity.compose)

    androidTestImplementation(project(":terminal-engine"))
    androidTestImplementation(project(":feature-terminal"))
    // TerminalSocketClient's own constructor (feature-terminal) takes
    // TerminalTicketSource/TerminalWebSocketFactory (data.terminal) as
    // public parameter types; feature-terminal's dependency on :data is
    // implementation-scoped, so LiveThroughputBenchmark needs its
    // own direct compile-time visibility of those interfaces to build a fake.
    androidTestImplementation(project(":data"))
    androidTestImplementation(libs.kotlinx.coroutines.core)
    androidTestImplementation(platform(libs.compose.bom))
    androidTestImplementation(libs.compose.ui)
    androidTestImplementation(libs.compose.foundation)
    androidTestImplementation(libs.androidx.activity.compose)
    androidTestImplementation(libs.androidx.test.core)
    androidTestImplementation(libs.junit)
    androidTestImplementation(libs.androidx.test.runner)
    androidTestImplementation(libs.androidx.test.ext.junit)
}
