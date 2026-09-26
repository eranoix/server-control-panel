// Type-resolution half of the "BFF-only network" gate. BffOnlyNetworkClientRule
// sees resolved types, so it catches what the lexical :convention plugin misses:
// routes built by concatenation or interpolation, and fully qualified references
// with no import. Consumed as a regular `detektPlugins` dependency via the
// group/name below.
plugins {
    id("org.jetbrains.kotlin.jvm") version "2.4.10"
}

group = "com.vpsmanager.buildlogic"
version = "unespecified"

kotlin {
    jvmToolchain(17)
}

// No repositories{} block: build-logic/settings.gradle.kts uses
// FAIL_ON_PROJECT_REPOS, so one here would fail the build.

val detektVersion = "1.23.8"

dependencies {
    compileOnly("io.gitlab.arturbosch.detekt:detekt-api:$detektVersion")

    testImplementation("io.gitlab.arturbosch.detekt:detekt-api:$detektVersion")
    testImplementation("io.gitlab.arturbosch.detekt:detekt-test:$detektVersion")
    testImplementation("io.gitlab.arturbosch.detekt:detekt-test-utils:$detektVersion")
    testImplementation("junit:junit:4.13.2")
}

tasks.test {
    useJUnit()
}
