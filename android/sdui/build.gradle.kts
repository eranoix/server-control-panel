// :sdui — renderer Compose do vocabulario fechado de 7 componentes
// (form/table/list/detail/action/chart/confirm_destructive). Sem motor de
// layout generico.
plugins {
    alias(libs.plugins.android.library)
    alias(libs.plugins.kotlin.compose)
}

android {
    namespace = "com.vpsmanager.sdui"
    compileSdk = libs.versions.compileSdk.get().toInt()

    defaultConfig {
        minSdk = libs.versions.minSdk.get().toInt()
    }

    // BuildConfig.DEBUG is what PayloadPreviewScreen self-gates on: it must
    // render nothing in a release build regardless of whether or when a
    // navigation graph ever calls it (plan 07-06 threat closure).
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
    // Component sources decode row/series JSON bodies directly (JsonObject,
    // JsonArray, jsonPrimitive) -- :data's own kotlinx-serialization-json
    // dependency is `implementation`-scoped there, so it is not exposed
    // transitively and must be declared here too.
    implementation(libs.kotlinx.serialization.json)

    testImplementation(libs.junit)
    testImplementation(libs.kotlin.reflect)
    testImplementation(libs.kotlinx.coroutines.test)
    // Renders the 7 SDUI components + NeedsUpdateCard under Robolectric,
    // driven from the same real committed fixtures the registry dispatch
    // tests already read via sdui.fixtures.dir -- never composed before this.
    testImplementation(libs.robolectric)
    testImplementation(libs.androidx.test.core)
    testImplementation(platform(libs.compose.bom))
    testImplementation(libs.compose.ui.test.junit4)
    debugImplementation(libs.compose.ui.test.manifest)
}

// The same corpus of real fixtures that :core reads (contracts/sdui/fixtures at
// the repo root) -- the registry's dispatch tests also run against the real
// files, never a copy pasted into the test source.
tasks.withType<Test> {
    val fixturesDir = rootDir.parentFile.resolve("contracts/sdui/fixtures")
    systemProperty("sdui.fixtures.dir", fixturesDir.absolutePath)
}
