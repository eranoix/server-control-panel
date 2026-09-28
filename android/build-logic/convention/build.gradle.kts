plugins {
    `kotlin-dsl`
}

gradlePlugin {
    plugins {
        register("noAndroidImportsInCore") {
            id = "dev.servercontrolpanel.no-android-imports-in-core"
            implementationClass = "NoAndroidImportsInCorePlugin"
        }
        register("bffOnlyNetwork") {
            id = "dev.servercontrolpanel.bff-only-network"
            implementationClass = "BffOnlyNetworkPlugin"
        }
    }
}
