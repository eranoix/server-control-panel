// Build convention: Gradle plugins that enforce, at build level (not by
// convention/comment):
// - that :core never imports android.*/androidx.*/okhttp3.*/retrofit2.* — the
//   "zero I/O, zero Android" contract of :core;
// - that no module outside the BFF (:data / :data:mobile-api-client) references
//   an /api/* route outside /api/mobile/v1 or imports okhttp3/retrofit2
//   directly. This is the lexical/textual half of the gate; the half
//   by type resolution lives in :build-logic:lint-rules (a detekt rule).
plugins {
    `kotlin-dsl`
}

gradlePlugin {
    plugins {
        register("noAndroidImportsInCore") {
            id = "com.vpsmanager.no-android-imports-in-core"
            implementationClass = "NoAndroidImportsInCorePlugin"
        }
        register("bffOnlyNetwork") {
            id = "com.vpsmanager.bff-only-network"
            implementationClass = "BffOnlyNetworkPlugin"
        }
    }
}
