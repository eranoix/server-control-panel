// :feature-jira — the Jira kanban board.
//
// A NATIVE module, and no longer an SDUI screen: the SDUI vocabulary is closed
// at 7 types, and the package's own comment says that a screen needing what
// none of them expresses is a sign of a native module. Drag-and-drop between
// columns, with automatic scrolling at the edge and undo when Jira refuses, is
// not describable in JSON.
//
// What IS describable — which columns exist and which cards fall into each one
// — still comes from the server (internal/mobilebff/jira_quadro.go), so
// changing the board's columns still does not require a new version of the app.
plugins {
    alias(libs.plugins.android.library)
    alias(libs.plugins.kotlin.compose)
}

android {
    namespace = "com.vpsmanager.feature.jira"
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
    implementation(project(":design-system"))

    implementation(platform(libs.compose.bom))
    implementation(libs.compose.runtime)
    implementation(libs.compose.ui)
    implementation(libs.compose.foundation)
    implementation(libs.compose.material3)
    // The "core" icon set (~48 glyphs). The "extended" one is 35.7 MB and is
    // deliberately out of the project — see the note in libs.versions.toml.
    implementation(libs.compose.material.icons.core)
    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.lifecycle.runtime.compose)
    implementation(libs.kotlinx.coroutines.core)

    testImplementation(libs.junit)
    testImplementation(libs.kotlinx.coroutines.test)
    // Renders this module's Compose screens under Robolectric — there is no
    // emulator in this environment, and a board screen with no render test
    // would be exactly the kind of piece that arrives crooked on the device.
    testImplementation(libs.robolectric)
    testImplementation(libs.androidx.test.core)
    testImplementation(platform(libs.compose.bom))
    testImplementation(libs.compose.ui.test.junit4)
    debugImplementation(libs.compose.ui.test.manifest)
}
