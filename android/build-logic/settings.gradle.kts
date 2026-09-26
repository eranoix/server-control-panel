pluginManagement {
    repositories {
        google()
        mavenCentral()
        gradlePluginPortal()
    }
}

dependencyResolutionManagement {
    repositoriesMode.set(RepositoriesMode.FAIL_ON_PROJECT_REPOS)
    repositories {
        google()
        mavenCentral()
    }
}

rootProject.name = "build-logic"

include(":convention")

// Type-resolving detekt rule that catches bypasses the lexical gate in
// :convention cannot see. Separate module because it depends on detekt-api
// and has its own tests.
include(":lint-rules")
