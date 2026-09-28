plugins {
    id("org.jetbrains.kotlin.jvm") version "2.4.10"
}

group = "dev.servercontrolpanel.buildlogic"
version = "unespecified"

kotlin {
    jvmToolchain(17)
}

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
