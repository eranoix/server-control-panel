// A pure Kotlin/JVM module: domain models, the SDUI contract and use-case
// interfaces. It never applies com.android.library and never depends on
// okhttp/retrofit — the purity is enforced mechanically by checkNoAndroidImports
// (it fails the build, it is not just a convention) via NoAndroidImportsInCorePlugin.
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
    // Only needed by tests, for KClass::sealedSubclasses (the Kotlin/Go
    // vocabulary-arity guard) — kotlin-reflect is a JetBrains-published
    // toolchain artifact, not a new third-party dependency.
    testImplementation(libs.kotlin.reflect)
}

// The SDUI fixture corpus lives at the repo root, outside the Android Gradle
// tree — the tests read the real files from disk (never a copy), so that an
// out-of-date fixture never passes silently.
tasks.test {
    val fixturesDir = rootDir.parentFile.resolve("contracts/sdui/fixtures")
    systemProperty("sdui.fixtures.dir", fixturesDir.absolutePath)
}
