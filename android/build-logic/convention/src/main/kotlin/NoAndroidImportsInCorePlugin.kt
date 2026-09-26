import org.gradle.api.DefaultTask
import org.gradle.api.GradleException
import org.gradle.api.Plugin
import org.gradle.api.Project
import org.gradle.api.tasks.Internal
import org.gradle.api.tasks.TaskAction
import org.gradle.kotlin.dsl.register
import java.io.File

/**
 * :core's whole contract is zero I/O, zero Android — these import prefixes
 * are the mechanical definition of "violates that contract".
 */
private val FORBIDDEN_IMPORT_PREFIXES = listOf(
    "import android.",
    "import androidx.",
    "import okhttp3.",
    "import retrofit2.",
)

abstract class CheckNoAndroidImportsTask : DefaultTask() {

    // Always re-runs (no up-to-date caching): this is a correctness gate, not
    // a task worth the complexity of incremental input tracking.
    @get:Internal
    var kotlinSourceFiles: List<File> = emptyList()

    @TaskAction
    fun check() {
        val violations = mutableListOf<String>()
        kotlinSourceFiles
            .filter { it.isFile && it.extension == "kt" }
            .forEach { file ->
                file.readLines().forEachIndexed { index, rawLine ->
                    val line = rawLine.trim()
                    if (line.startsWith("//")) return@forEachIndexed
                    if (FORBIDDEN_IMPORT_PREFIXES.any { prefix -> line.startsWith(prefix) }) {
                        violations += "${file.path}:${index + 1}: $line"
                    }
                }
            }
        if (violations.isNotEmpty()) {
            throw GradleException(
                "core module has forbidden android.*/androidx.*/okhttp3.*/retrofit2.* " +
                    "import(s) — zero I/O, zero Android is core's whole contract. " +
                    "Violations:\n" + violations.joinToString("\n") { "  - $it" }
            )
        }
    }
}

/**
 * Registers checkNoAndroidImports and wires it into the check lifecycle task
 * so a violation is a real build failure, not a lint warning that can be
 * ignored.
 */
class NoAndroidImportsInCorePlugin : Plugin<Project> {
    override fun apply(project: Project) {
        val sourceTree = project.fileTree(project.file("src/main/kotlin"))
        sourceTree.include("**/*.kt")

        val checkTask = project.tasks.register<CheckNoAndroidImportsTask>("checkNoAndroidImports") {
            group = "verification"
            description = "Fails the build if this module imports android.*/androidx.*/okhttp3.*/retrofit2.*"
            kotlinSourceFiles = sourceTree.files.toList()
        }
        project.tasks.named("check") {
            dependsOn(checkTask)
        }
    }
}
