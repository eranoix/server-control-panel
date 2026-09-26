// :feature-files — navegador/editor de arquivos remoto.
plugins {
    alias(libs.plugins.android.library)
    alias(libs.plugins.kotlin.compose)
}

android {
    namespace = "com.vpsmanager.feature.files"
    compileSdk = libs.versions.compileSdk.get().toInt()

    defaultConfig {
        minSdk = libs.versions.minSdk.get().toInt()
        // LGPL-2.1 obligation (sora-editor, io.github.rosemoe): a consumer's
        // R8 pass must never obfuscate/strip this library, so a user could
        // relink a modified copy of it. Declared here (the module that
        // actually depends on sora-editor) so it auto-merges into :app's
        // R8 config whenever minification is enabled there, present or future.
        consumerProguardFiles("consumer-rules.pro")
    }

    // sora-editor's own artifacts ship JVM-17-targeted bytecode with inline
    // functions (e.g. CodeEditor.subscribeAlways); Kotlin cannot inline
    // JVM-17 bytecode into a module compiling at a lower target, so this
    // module must compile at the same target (same pattern as :data, which
    // hit the identical error inlining :data:mobile-api-client).
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
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
    // Icons from the "core" set (823 KB) -- only the back arrow on the detail
    // bar. See the note in libs.versions.toml about not using the extended set.
    implementation(libs.compose.material.icons.core)
    implementation(libs.androidx.activity.compose)
    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.lifecycle.runtime.compose)
    implementation(libs.kotlinx.coroutines.core)

    // WorkManager: the transfer engine (download/upload) — durable background
    // execution that survives process death, with native progress/cancel via
    // WorkInfo.
    implementation(libs.androidx.work.runtime.ktx)

    // sora-editor (LGPL-2.1-or-later, io.github.rosemoe) -- consumed
    // exclusively via its Maven Central coordinates through the BOM, never
    // vendored/forked into this repository. See OssLicensesScreen (:app)
    // for the in-app attribution this obligates.
    implementation(platform(libs.sora.editor.bom))
    implementation(libs.sora.editor)
    implementation(libs.sora.editor.language.textmate)

    testImplementation(libs.junit)
    testImplementation(libs.kotlinx.coroutines.test)
    // Renders this module's Compose screens under Robolectric (no emulator or
    // device available in this environment) — never exercised before.
    testImplementation(libs.robolectric)
    testImplementation(libs.androidx.test.core)
    testImplementation(platform(libs.compose.bom))
    testImplementation(libs.compose.ui.test.junit4)
    debugImplementation(libs.compose.ui.test.manifest)
    testImplementation(libs.androidx.work.testing)
}
