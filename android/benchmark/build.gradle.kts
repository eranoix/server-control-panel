plugins {
    alias(libs.plugins.android.library)
    alias(libs.plugins.kotlin.compose)
}

android {
    namespace = "dev.servercontrolpanel.benchmark"
    compileSdk = libs.versions.compileSdk.get().toInt()

    defaultConfig {
        minSdk = libs.versions.minSdk.get().toInt()
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
    }

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
