// :feature-admin — a thin SDUI host: given a section id, it calls the BFF for a
// screen descriptor and hands it to :sdui. No per-section logic.
plugins {
    alias(libs.plugins.android.library)
    alias(libs.plugins.kotlin.compose)
}

android {
    namespace = "com.vpsmanager.feature.admin"
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
    // :design-system brings the glyphs the `material-icons-core` set does not
    // have (memory, disk, layers, speedometer) — the ones that give each
    // section its identity in the launcher. See IconeDaSecao.
    implementation(project(":design-system"))
    implementation(project(":data"))
    implementation(project(":sdui"))
    implementation(platform(libs.compose.bom))
    implementation(libs.compose.runtime)
    implementation(libs.compose.ui)
    implementation(libs.compose.foundation)
    implementation(libs.compose.material3)
    // Only the CORE icon package (~48 symbols, 823 KB). The extended one weighs
    // 35.7 MB and none of its icons are used here — the launcher draws the
    // group's initial precisely so as not to depend on a symbol catalogue that
    // would have to grow with every new section the server adds.
    implementation(libs.compose.material.icons.core)
    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.lifecycle.runtime.compose)
    implementation(libs.kotlinx.coroutines.core)
    // MobileEvent.data / SDUI screen decoding both need JsonElement/JsonObject on this
    // module's own compile classpath — :data declares this as `implementation`, not `api`,
    // so it does not come along transitively through project(":data").
    implementation(libs.kotlinx.serialization.json)

    testImplementation(libs.junit)
    testImplementation(libs.robolectric)
    testImplementation(libs.androidx.test.core)
    testImplementation(libs.kotlinx.coroutines.test)
    testImplementation(platform(libs.compose.bom))
    testImplementation(libs.compose.ui.test.junit4)
    debugImplementation(libs.compose.ui.test.manifest)
}
