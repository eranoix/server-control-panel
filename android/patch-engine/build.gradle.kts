// :patch-engine — rebuilds the new APK from the installed APK plus an
// HDiffPatch binary patch, and checks the SHA-256 of what it produced. A file
// goes in, a file comes out: no networking, no Compose, no installer. It
// mirrors :terminal-engine, this project's other module with a JNI/NDK border.
//
// The native side comes from vendored SOURCE (vendor/, see
// vendor-hdiffpatch.sh), never from a third party's .so/.aar — this code runs
// over an APK that is going to be INSTALLED, so its supply chain matters as
// much as the result.
plugins {
    alias(libs.plugins.android.library)
}

android {
    namespace = "com.vpsmanager.patchengine"
    compileSdk = libs.versions.compileSdk.get().toInt()
    ndkVersion = libs.versions.ndk.get()

    defaultConfig {
        minSdk = libs.versions.minSdk.get().toInt()
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"

        ndk {
            abiFilters += listOf("arm64-v8a", "x86_64")
        }
    }

    // ndkBuild, and not CMake as in :terminal-engine. The difference is
    // deliberate and justified at the top of src/main/cpp/Android.mk: there
    // what gets vendored is a static library already built by a script of its
    // own, here it is upstream's .mk — with the switches that decide which
    // decompressors exist inside the .so. Retranslating those -D flags into
    // CMake would create a second place to drift from upstream, and the drift
    // would not show up in the build: it would show up as a refused patch on
    // the user's device.
    externalNativeBuild {
        ndkBuild {
            path = file("src/main/cpp/Android.mk")
        }
    }

    sourceSets {
        getByName("main") {
            // com.github.sisong.HPatch — the official binding, vendored
            // LITERALLY. The JNI symbol compiled in hpatch_jni.c is
            // Java_com_github_sisong_HPatch_patch: moving the class to another
            // package would break the link at runtime, not at compile time.
            // That is why it is compiled from where it was vendored, instead
            // of being copied into src/ (a copy ages silently).
            java.srcDir("vendor/HDiffPatch/builds/android_ndk_jni_mk/java")
        }
        getByName("androidTest") {
            // Fixtures for the real-cycle test (two signed ~33 MB APKs and
            // the patch between them). They are NOT versioned: they regenerate
            // in minutes via tools/make-patch-fixtures.sh and 66 MB of binary
            // living forever in the repository does not pay for itself. See
            // the KDoc of ApkPatcherRealApkTest.
            // .asFile and not the Provider: AGP 9's SourceSet API refuses a
            // Provider (it cannot decide whether it points at a generated or a
            // static directory). Here it is static in fact — the script is
            // what writes it, outside Gradle.
            assets.srcDir(layout.buildDirectory.dir("patch-fixtures").get().asFile)
        }
    }

    testOptions {
        unitTests.isIncludeAndroidResources = true
    }
}

dependencies {
    testImplementation(libs.junit)

    androidTestImplementation(libs.junit)
    androidTestImplementation(libs.androidx.test.runner)
    androidTestImplementation(libs.androidx.test.ext.junit)
}
