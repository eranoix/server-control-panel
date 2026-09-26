// Pure Kotlin/JVM module: domain models, the SDUI contract and use-case interfaces.
// No Android or okhttp/retrofit dependencies; NoAndroidImportsInCorePlugin fails the build otherwise.
plugins {
    alias(libs.plugins.kotlin.jvm)
    alias(libs.plugins.kotlin.serialization)
    id("com.vpsmanager.no-android-imports-in-core")
}

kotlin {
    jvmToolchain(17)
}

dependencies {
    implementation(libs.kotlinx.serialization.json)

    testImplementation(libs.junit)
    // Tests only: KClass::sealedSubclasses for the Kotlin/Go vocabulary-arity guard.
    testImplementation(libs.kotlin.reflect)
}

// Tests read the real SDUI fixtures from the repo root (never a copy), so a
// stale fixture cannot pass silently.
tasks.test {
    val fixturesDir = rootDir.parentFile.resolve("contracts/sdui/fixtures")
    systemProperty("sdui.fixtures.dir", fixturesDir.absolutePath)
}
