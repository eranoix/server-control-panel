// :feature-notifications — caixa de entrada de notificacoes, canal push.
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
    // :core brings the bridge to the terminal — the primary action of every
    // alert in the inbox is to take it to the place that answers any
    // question (ComandosDaPonte).
    implementation(project(":core"))
    // :design-system: the state colours (ok/warning/critical) come from ONE
    // place only. Two different reds on the same screen teach the eye that
    // red does not mean anything.
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
    // OnboardingDePush asks for POST_NOTIFICATIONS through the activity result
    // contract — without this the app declares the permission and never
    // requests it, which was the defect.
    implementation(libs.androidx.activity.compose)
    implementation(libs.kotlinx.coroutines.core)
    // WorkManager: following a ten-minute deploy has to survive closing the
    // app. It is also what promotes the work to the foreground, which is the
    // only use of a foreground service that Android 13/14/15 still accept
    // willingly: a start, visible progress and an end.
    implementation(libs.androidx.work.runtime.ktx)
    // VpsFirebaseMessagingService receives the data-only FCM message —
    // this is the one module in the app allowed to touch com.google.firebase.*,
    // mirroring how :data is the one module allowed to touch okhttp3/retrofit2
    // (the boundary covers HTTP only, not the separate FCM transport).
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
