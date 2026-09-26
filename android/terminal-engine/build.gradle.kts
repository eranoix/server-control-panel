// :terminal-engine — the JNI boundary to libghostty-vt. write(bytes) goes in, a
// grid snapshot() comes out; no Compose, no network. The native shim
// (src/main/cpp/ghostty_jni.cpp) is the ONLY translation unit allowed to
// reference ghostty_* types/enums — the C ABI vendored in vendor/include is
// explicitly unstable and pinned to a single commit.
plugins {
    alias(libs.plugins.android.library)
}

android {
    namespace = "com.vpsmanager.terminalengine"
    compileSdk = libs.versions.compileSdk.get().toInt()
    ndkVersion = libs.versions.ndk.get()

    defaultConfig {
        minSdk = libs.versions.minSdk.get().toInt()
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"

        ndk {
            abiFilters += listOf("arm64-v8a", "x86_64")
        }

        externalNativeBuild {
            cmake {
                cppFlags += "-std=c++17"
            }
        }
    }

    externalNativeBuild {
        cmake {
            path = file("src/main/cpp/CMakeLists.txt")
        }
    }

    testOptions {
        unitTests.isIncludeAndroidResources = true
    }
}

dependencies {
    testImplementation(libs.junit)
    testImplementation(libs.robolectric)

    androidTestImplementation(libs.junit)
    androidTestImplementation(libs.androidx.test.runner)
    androidTestImplementation(libs.androidx.test.ext.junit)
}
