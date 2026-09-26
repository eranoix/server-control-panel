// :feature-notifications: notification inbox and push channel.
plugins {
    alias(libs.plugins.android.library)
    alias(libs.plugins.kotlin.compose)
}

android {
    namespace = "com.vpsmanager.feature.notifications"
    compileSdk = libs.versions.compileSdk.get().toInt()

    defaultConfig {
        minSdk = libs.versions.minSdk.get().toInt()
    }

    testOptions {
        unitTests.isIncludeAndroidResources = true
    }
}

dependencies {
    // :core provides the terminal bridge used by each alert's primary action.
    implementation(project(":core"))
    // :design-system is the single source of the ok/warning/critical colours.
    implementation(project(":design-system"))
    implementation(project(":data"))
    implementation(platform(libs.compose.bom))
    implementation(libs.compose.runtime)
    implementation(libs.compose.ui)
    implementation(libs.compose.foundation)
    implementation(libs.compose.material3)
    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.lifecycle.runtime.compose)
    implementation(libs.androidx.core.ktx)
    // PushOnboarding requests POST_NOTIFICATIONS through the activity result contract.
    implementation(libs.androidx.activity.compose)
    implementation(libs.kotlinx.coroutines.core)
    // WorkManager: following a long deploy must survive closing the app, as a foreground
    // job with a start, visible progress and an end.
    implementation(libs.androidx.work.runtime.ktx)
    // The only module allowed to use com.google.firebase.*, as :data is for HTTP.
    implementation(platform(libs.firebase.bom))
    implementation(libs.firebase.messaging)

    testImplementation(libs.junit)
    testImplementation(libs.robolectric)
    testImplementation(libs.androidx.test.core)
    testImplementation(libs.kotlinx.coroutines.test)
    testImplementation(platform(libs.compose.bom))
    testImplementation(libs.compose.ui.test.junit4)
    debugImplementation(libs.compose.ui.test.manifest)
}
