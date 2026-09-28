plugins {
    alias(libs.plugins.kotlin.jvm) apply false
    alias(libs.plugins.kotlin.serialization) apply false
    alias(libs.plugins.kotlin.compose) apply false
    alias(libs.plugins.android.application) apply false
    alias(libs.plugins.android.library) apply false
    alias(libs.plugins.openapi.generator) apply false
    id("dev.servercontrolpanel.bff-only-network") apply false
    alias(libs.plugins.detekt) apply false
}

val bffOnlyNetworkExemptModules = setOf(":data", ":data:mobile-api-client")

subprojects {
    if (path !in bffOnlyNetworkExemptModules) {
        apply(plugin = "dev.servercontrolpanel.bff-only-network")

        apply(plugin = "io.gitlab.arturbosch.detekt")

        extensions.configure<io.gitlab.arturbosch.detekt.extensions.DetektExtension> {
            buildUponDefaultConfig = false
            config.setFrom(rootProject.file("config/detekt/detekt.yml"))
            source.setFrom(files("src/main/kotlin"))
        }

        dependencies {
            add("detektPlugins", "dev.servercontrolpanel.buildlogic:lint-rules:1.0")
        }

        tasks.withType<io.gitlab.arturbosch.detekt.Detekt>().configureEach {
            val detektTask = this
            detektTask.jvmTarget = "17"

            project.configurations.matching { it.name == "compileClasspath" }.configureEach {
                detektTask.classpath.from(this)
            }

            project.configurations.matching { it.name == "debugCompileClasspath" }.configureEach {
                val artifactType = org.gradle.api.attributes.Attribute.of("artifactType", String::class.java)
                detektTask.classpath.from(
                    incoming.artifactView {
                        attributes { attribute(artifactType, "android-classes-jar") }
                        isLenient = true
                    }.files
                )
            }
        }

        tasks.matching { it.name == "check" }.configureEach {
            dependsOn("detekt")
        }
    }
}
