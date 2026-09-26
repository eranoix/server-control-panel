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

// Custom detekt rule with type resolution -- it closes the class of
// bypass the lexical gate in :convention cannot see (concatenation,
// interpolation, fully qualified reference with no import). A separate
// module, not a subdirectory of :convention, because it consumes detekt-api
// (a real dependency, not just a build plugin) and has its own tests.
include(":lint-rules")
