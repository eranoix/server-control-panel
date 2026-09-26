// The type-resolution half of the "BFF-only network" gate. The
// detekt rule in here (BffOnlyNetworkClientRule) sees the RESOLVED
// type of an expression, not the source text -- and that is why it catches the class
// of bypass the lexical Gradle plugin in :convention does not: a route
// built by concatenation/interpolation, or a fully
// qualified reference with no import (e.g. okhttp3.OkHttpClient() without `import okhttp3.*`).
//
// A pure Kotlin/JVM module, consumed by the main build as an ordinary
// `detektPlugins` dependency (not a build plugin like :convention) -- Gradle's
// dependency substitution for included builds resolves that
// automatically from the group/name declared below.
plugins {
    id("org.jetbrains.kotlin.jvm") version "2.4.10"
}

group = "com.vpsmanager.buildlogic"
version = "unespecified"

kotlin {
    jvmToolchain(17)
}

// Repositories come from dependencyResolutionManagement in
// build-logic/settings.gradle.kts (FAIL_ON_PROJECT_REPOS) -- a
// repositories{} block here conflicts with that mode and fails the build.

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
