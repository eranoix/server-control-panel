plugins {
    alias(libs.plugins.kotlin.jvm)
    alias(libs.plugins.kotlin.serialization)
    alias(libs.plugins.openapi.generator)
}

java {
    toolchain {
        languageVersion.set(JavaLanguageVersion.of(17))
    }
}

val generatedSrcDir = layout.buildDirectory.dir("generated/openapi")

openApiGenerate {
    generatorName.set("kotlin")
    inputSpec.set("$projectDir/openapi/mobile-v1.yaml")
    outputDir.set(generatedSrcDir)
    templateDir.set("$projectDir/openapi-templates")
    packageName.set("dev.servercontrolpanel.mobileapiclient")
    apiPackage.set("dev.servercontrolpanel.mobileapiclient.api")
    modelPackage.set("dev.servercontrolpanel.mobileapiclient.model")
    invokerPackage.set("dev.servercontrolpanel.mobileapiclient.invoker")
    configOptions.set(
        mapOf(
            "library" to "jvm-okhttp4",
            "serializationLibrary" to "kotlinx_serialization",
            "dateLibrary" to "java8",
            "useCoroutines" to "true",
        )
    )
}

sourceSets {
    main {
        kotlin.srcDir(generatedSrcDir.map { it.dir("src/main/kotlin") })
    }
}

tasks.named("compileKotlin") {
    dependsOn("openApiGenerate")
}

dependencies {
    api(libs.okhttp)
    api(libs.kotlinx.serialization.json)
    api(libs.kotlinx.coroutines.core)
}
