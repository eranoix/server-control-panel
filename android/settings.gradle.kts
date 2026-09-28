pluginManagement {
    includeBuild("build-logic")
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

rootProject.name = "server-control-panel-android"

includeBuild("build-logic")

include(":data:mobile-api-client")

include(":core")

include(":data")
include(":sdui")
include(":terminal-engine")
include(":patch-engine")
include(":design-system")

include(":benchmark")

include(":feature-terminal")
project(":feature-terminal").projectDir = file("feature/terminal")
include(":feature-admin")
project(":feature-admin").projectDir = file("feature/admin")
include(":feature-auth")
project(":feature-auth").projectDir = file("feature/auth")
include(":feature-notifications")
project(":feature-notifications").projectDir = file("feature/notifications")
include(":feature-videocall")
project(":feature-videocall").projectDir = file("feature/videocall")
include(":feature-whatsapp")
project(":feature-whatsapp").projectDir = file("feature/whatsapp")
include(":feature-files")
project(":feature-files").projectDir = file("feature/files")
include(":feature-jira")
project(":feature-jira").projectDir = file("feature/jira")

include(":app")
