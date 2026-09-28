plugins {
    alias(libs.plugins.kotlin.jvm)
    alias(libs.plugins.kotlin.serialization)
    id("dev.servercontrolpanel.no-android-imports-in-core")
}

kotlin {
    jvmToolchain(17)
}

dependencies {
    implementation(libs.kotlinx.serialization.json)

    testImplementation(libs.junit)
    testImplementation(libs.kotlin.reflect)
}

tasks.test {
    val fixturesDir = rootDir.parentFile.resolve("contracts/sdui/fixtures")
    systemProperty("sdui.fixtures.dir", fixturesDir.absolutePath)
}
