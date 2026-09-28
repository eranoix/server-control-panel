plugins {
    alias(libs.plugins.android.library)
}

android {
    namespace = "dev.servercontrolpanel.patchengine"
    compileSdk = libs.versions.compileSdk.get().toInt()
    ndkVersion = libs.versions.ndk.get()

    defaultConfig {
        minSdk = libs.versions.minSdk.get().toInt()
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"

        ndk {
            abiFilters += listOf("arm64-v8a", "x86_64")
        }
    }

    externalNativeBuild {
        ndkBuild {
            path = file("src/main/cpp/Android.mk")
        }
    }

    sourceSets {
        getByName("main") {
            java.srcDir("vendor/HDiffPatch/builds/android_ndk_jni_mk/java")
        }
        getByName("androidTest") {
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
