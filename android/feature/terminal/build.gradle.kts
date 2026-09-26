// :feature-terminal — the terminal screen: session list/resume, /ws/shell
// lifecycle, hosts the Compose Canvas renderer that consumes snapshots from
// :terminal-engine. The transport (TerminalSocketClient) depends on :data only
// through the pure interfaces in com.vpsmanager.data.terminal
// (TerminalRepository, TerminalWebSocketFactory) — it never imports
// okhttp3/retrofit2 directly; the real OkHttp implementation lives whole in :data.
plugins {
    alias(libs.plugins.android.library)
    alias(libs.plugins.kotlin.compose)
    alias(libs.plugins.kotlin.serialization)
}

android {
    namespace = "com.vpsmanager.feature.terminal"
    compileSdk = libs.versions.compileSdk.get().toInt()

    defaultConfig {
        minSdk = libs.versions.minSdk.get().toInt()
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"

        // Forwards `-Psoak.hours=N` to SoakTest as an instrumentation
        // argument (`InstrumentationRegistry.getArguments().getString("soak.hours")`),
        // so the multi-hour soak run and everyday quick test runs use the
        // exact same `connectedDebugAndroidTest` invocation, only the
        // property differs. Absent, SoakTest falls back to its own short
        // smoke-only default.
        project.findProperty("soak.hours")?.let { hours ->
            testInstrumentationRunnerArguments["soak.hours"] = hours.toString()
        }
    }

    testOptions {
        unitTests.isIncludeAndroidResources = true
    }

    // The VT fixture corpus (.vt + .expected.json) lives in :terminal-engine's
    // androidTest assets (next to the rest of what that module already exposes
    // for instrumented tests). VtConformanceTest lives here because only here
    // does the Compose renderer it exercises exist, so this androidTest
    // sourceSet points at the other module's assets folder instead of
    // duplicating the fixtures.
    sourceSets {
        getByName("androidTest") {
            assets.srcDirs("../../terminal-engine/src/androidTest/assets")
        }
    }
}

dependencies {
    implementation(project(":terminal-engine"))
    implementation(project(":data"))
    // :core brings the shell quoting (com.vpsmanager.core.shell) used when
    // inserting an attachment's path into the command line. It lives in :core,
    // and not here, because it is pure logic and its test runs a real /bin/sh —
    // no Android involved.
    implementation(project(":core"))
    // :design-system brings the terminal icon (VpsmIcons, drawn by hand so as
    // not to drag in the 35.7 MB material-icons-extended) and the state colours
    // the session list uses on the "active" badge.
    implementation(project(":design-system"))
    implementation(platform(libs.compose.bom))
    implementation(libs.compose.runtime)
    implementation(libs.compose.ui)
    implementation(libs.compose.foundation)
    implementation(libs.compose.material3)
    // Icons from the "core" set (823 KB) -- only the back arrow on the detail
    // bar. See the note in libs.versions.toml about not using the extended set.
    implementation(libs.compose.material.icons.core)
    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.lifecycle.runtime.compose)
    implementation(libs.kotlinx.coroutines.core)
    implementation(libs.kotlinx.serialization.json)
    implementation(libs.androidx.datastore.preferences)
    // Attachments from the terminal: the system pickers (file, gallery, camera)
    // are ActivityResult contracts, and the upload itself runs in WorkManager —
    // durable, resumable, surviving both leaving the screen and process death
    // (that is what makes uploading work on a bad connection).
    implementation(libs.androidx.activity.compose)
    implementation(libs.androidx.work.runtime.ktx)

    testImplementation(libs.junit)
    testImplementation(libs.androidx.work.testing)
    testImplementation(libs.robolectric)
    testImplementation(libs.androidx.test.core)
    testImplementation(libs.kotlinx.coroutines.test)
    testImplementation(platform(libs.compose.bom))
    testImplementation(libs.compose.ui.test.junit4)

    androidTestImplementation(project(":terminal-engine"))
    androidTestImplementation(libs.junit)
    androidTestImplementation(libs.androidx.test.runner)
    androidTestImplementation(libs.androidx.test.ext.junit)
    androidTestImplementation(platform(libs.compose.bom))
    androidTestImplementation(libs.compose.ui.test.junit4)
    // ComponentActivity host for SelectionComposeIndependenceTest's
    // createAndroidComposeRule<ComponentActivity>() — real touch-gesture
    // dispatch needs a real Activity, not just the Compose UI test harness.
    androidTestImplementation(libs.androidx.activity.compose)
    debugImplementation(libs.compose.ui.test.manifest)
}
