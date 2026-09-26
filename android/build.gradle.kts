// Root Gradle project of the native Android app. It contains just enough to
// prove the round-trip of the generated OpenAPI client (:data:mobile-api-client).
// AGP is declared here (apply false) for the app/feature modules that future
// work adds — :data:mobile-api-client is a pure Kotlin/JVM module
// and does not use AGP.
plugins {
    alias(libs.plugins.kotlin.jvm) apply false
    alias(libs.plugins.kotlin.serialization) apply false
    alias(libs.plugins.kotlin.compose) apply false
    alias(libs.plugins.android.application) apply false
    alias(libs.plugins.android.library) apply false
    alias(libs.plugins.openapi.generator) apply false
    // apply false here so the plugin id resolves onto this build's classpath
    // via the build-logic included-build substitution -- the subprojects
    // block below then applies it imperatively per module. The declarative
    // `plugins { id(...) }` form is what triggers composite-build plugin
    // resolution; a bare `apply(plugin = ...)` with no prior declarative
    // reference to the id does not.
    id("com.vpsmanager.bff-only-network") apply false
    // Same reason as the apply false above -- it resolves the id on the classpath before
    // the subprojects{} below applies it imperatively.
    alias(libs.plugins.detekt) apply false
}

// The only legal HTTP surface of the app is /api/mobile/v1/* through the
// client generated in :data:mobile-api-client. Every module outside this set
// -- including :core and any module future work adds -- is
// covered by default, with no per-module enumeration/opt-in. A module entering
// this exception is a security change reviewable in a single line, never
// a silent side effect of adding a new module.
val bffOnlyNetworkExemptModules = setOf(":data", ":data:mobile-api-client")

subprojects {
    if (path !in bffOnlyNetworkExemptModules) {
        // BffOnlyNetworkPlugin itself registers checkBffOnlyNetwork and wires
        // it into this module's check task -- the subprojects conditional
        // above is the whole "every module by default, no per-module
        // enumeration" mechanism; nothing more is needed here.
        apply(plugin = "com.vpsmanager.bff-only-network")

        // The type-resolution half of the same gate -- it sees the RESOLVED
        // type of an expression, not the source text, closing the class of
        // bypasses (concatenation, fully qualified reference) that the
        // lexical plugin above cannot see. The rule lives in
        // build-logic/lint-rules, applied here via detektPlugins.
        apply(plugin = "io.gitlab.arturbosch.detekt")

        extensions.configure<io.gitlab.arturbosch.detekt.extensions.DetektExtension> {
            // false on purpose -- only this gate's custom rule runs here.
            // detekt's default baseline (style, naming, complexity) is
            // out of scope for this gate and would break existing files that
            // no task here has touched.
            buildUponDefaultConfig = false
            config.setFrom(rootProject.file("config/detekt/detekt.yml"))
            source.setFrom(files("src/main/kotlin"))
        }

        dependencies {
            // The version here is ignored -- Gradle's dependency substitution
            // for included builds resolves by group:name, not by
            // version (build-logic/settings.gradle.kts includes :lint-rules).
            add("detektPlugins", "com.vpsmanager.buildlogic:lint-rules:1.0")
        }

        // The `detekt` task (no suffix) only runs with type resolution when
        // its `classpath` is not empty -- and the automatic integration of the
        // detekt 1.23.x plugin that fills this in by itself (`detektMain` for
        // pure Kotlin/JVM, `detekt<Variant>` for Android through the old
        // variant API `com.android.build.gradle.api.BaseVariant`) does not
        // fire on Android modules here: AGP 9.x no longer exposes that
        // legacy API, so no `detekt<Variant>` task ever gets created
        // (confirmed: `:feature-admin:tasks --all` does not list `detektDebug`).
        // Filling the classpath by hand on the `detekt` task itself works
        // the same for both module shapes and avoids depending on an
        // integration that only really exists for pure Kotlin/JVM.
        tasks.withType<io.gitlab.arturbosch.detekt.Detekt>().configureEach {
            val detektTask = this
            detektTask.jvmTarget = "17"

            // Pure Kotlin/JVM modules (e.g. :core): compileClasspath publishes only
            // one artifactType per dependency, so resolving the raw Configuration
            // as a FileCollection was never ambiguous here -- behaviour already
            // proven for :core.
            project.configurations.matching { it.name == "compileClasspath" }.configureEach {
                detektTask.classpath.from(this)
            }

            // Android modules (debugCompileClasspath): project dependencies
            // (e.g. :data) publish SEVERAL secondary variants of the same
            // configuration (android-classes-jar, jar, r-class-jar, android-lint,
            // android-manifest, ...) told apart only by the `artifactType`
            // attribute. Adding the raw Configuration as a FileCollection
            // asks for "any artifactType", which Gradle refuses to resolve as
            // ambiguous (`Could not resolve all dependencies for configuration
            // ':X:debugCompileClasspath'`) as soon as the dependency graph
            // includes an Android project module -- the real Kotlin compilation task
            // does not use the raw Configuration, it uses an ArtifactView with that
            // attribute asked for explicitly, so it never hits the problem.
            // We reproduce the same ArtifactView here.
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
