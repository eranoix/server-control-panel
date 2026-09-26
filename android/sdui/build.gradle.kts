// Compose renderer for the closed 7-component SDUI vocabulary. No generic layout engine.
plugins {
    alias(libs.plugins.android.library)
    alias(libs.plugins.kotlin.compose)
}

android {
    namespace = "dev.servercontrolpanel.sdui"
    compileSdk = libs.versions.compileSdk.get().toInt()

    defaultConfig {
        minSdk = libs.versions.minSdk.get().toInt()
    }

    // PayloadPreviewScreen gates on BuildConfig.DEBUG so it renders nothing in release builds.
    buildFeatures {
        buildConfig = true
    }

    testOptions {
        unitTests.isIncludeAndroidResources = true
    }
}

dependencies {
    implementation(project(":core"))
    implementation(project(":data"))
    implementation(project(":design-system"))
    implementation(platform(libs.compose.bom))
    implementation(libs.compose.runtime)
    implementation(libs.compose.ui)
    implementation(libs.compose.foundation)
    implementation(libs.compose.material3)
    implementation(libs.kotlinx.coroutines.core)
    // Needed directly: :data declares serialization-json as `implementation`, not `api`.
    implementation(libs.kotlinx.serialization.json)

    testImplementation(libs.junit)
    testImplementation(libs.kotlin.reflect)
    testImplementation(libs.kotlinx.coroutines.test)
    testImplementation(libs.robolectric)
    testImplementation(libs.androidx.test.core)
    testImplementation(platform(libs.compose.bom))
    testImplementation(libs.compose.ui.test.junit4)
    debugImplementation(libs.compose.ui.test.manifest)
}

// Tests read the same real SDUI fixtures as :core, never a copy.
tasks.withType<Test> {
    val fixturesDir = rootDir.parentFile.resolve("contracts/sdui/fixtures")
    systemProperty("sdui.fixtures.dir", fixturesDir.absolutePath)
}
