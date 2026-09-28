import org.gradle.api.DefaultTask
import org.gradle.api.GradleException
import org.gradle.api.Plugin
import org.gradle.api.Project
import org.gradle.api.tasks.Internal
import org.gradle.api.tasks.TaskAction
import org.gradle.kotlin.dsl.register
import java.io.File

private val FORBIDDEN_IMPORT_PREFIXES = listOf(
    "import android.",
    "import androidx.",
    "import okhttp3.",
    "import retrofit2.",
)

abstract class CheckNoAndroidImportsTask : DefaultTask() {

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
