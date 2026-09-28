import org.gradle.api.DefaultTask
import org.gradle.api.GradleException
import org.gradle.api.Plugin
import org.gradle.api.Project
import org.gradle.api.tasks.Internal
import org.gradle.api.tasks.TaskAction
import org.gradle.kotlin.dsl.register
import java.io.File

private val FORBIDDEN_IMPORT_PREFIXES = listOf(
    "import okhttp3.",
    "import retrofit2.",
)

private val WS_EXEMPT_SUBSTRINGS = listOf("/ws/shell", "/ws/videocall")

private val API_LITERAL_PATTERN = Regex("/api/[\\w./\\-]*")
private const val ALLOWED_API_PREFIX = "/api/mobile/v1"

internal class KotlinSourceScan(
    val codeOnlyLines: List<String>,
    val stringLiterals: List<Pair<Int, String>>,
)

internal fun scanKotlinSource(text: String): KotlinSourceScan {
    val code = StringBuilder()
    val literals = mutableListOf<Pair<Int, String>>()
    var i = 0
    var line = 1
    val n = text.length
    fun peek(offset: Int = 0): Char? = text.getOrNull(i + offset)

    while (i < n) {
        val c = text[i]
        when {
            c == '\n' -> {
                code.append('\n')
                line++
                i++
            }
            c == '/' && peek(1) == '/' -> {
                while (i < n && text[i] != '\n') i++
            }
            c == '/' && peek(1) == '*' -> {
                i += 2
                while (i < n && !(text[i] == '*' && peek(1) == '/')) {
                    if (text[i] == '\n') {
                        code.append('\n')
                        line++
                    }
                    i++
                }
                if (i < n) i += 2
            }
            c == '"' && peek(1) == '"' && peek(2) == '"' -> {
                val startLine = line
                i += 3
                val sb = StringBuilder()
                while (i < n && !(text[i] == '"' && peek(1) == '"' && peek(2) == '"')) {
                    if (text[i] == '\n') {
                        code.append('\n')
                        line++
                    }
                    sb.append(text[i])
                    i++
                }
                if (i < n) i += 3
                literals += startLine to sb.toString()
            }
            c == '"' -> {
                val startLine = line
                i++
                val sb = StringBuilder()
                while (i < n && text[i] != '"' && text[i] != '\n') {
                    if (text[i] == '\\' && i + 1 < n) {
                        sb.append(text[i]).append(text[i + 1])
                        i += 2
                        continue
                    }
                    sb.append(text[i])
                    i++
                }
                if (i < n && text[i] == '"') i++
                literals += startLine to sb.toString()
            }
            c == '\'' -> {
                i++
                if (i < n && text[i] == '\\') {
                    i += 2
                } else if (i < n) {
                    i++
                }
                if (i < n && text[i] == '\'') i++
            }
            else -> {
                code.append(c)
                i++
            }
        }
    }
    return KotlinSourceScan(code.toString().split("\n"), literals)
}

internal fun findBffOnlyNetworkViolations(file: File): List<String> {
    val scan = scanKotlinSource(file.readText())
    val violations = mutableListOf<String>()

    scan.codeOnlyLines.forEachIndexed { index, rawLine ->
        val trimmed = rawLine.trim()
        val matchedPrefix = FORBIDDEN_IMPORT_PREFIXES.firstOrNull { trimmed.startsWith(it) }
        if (matchedPrefix != null) {
            violations += "${file.path}:${index + 1}: forbidden import outside the BFF: $trimmed"
        }
    }

    scan.stringLiterals.forEach { (lineNo, content) ->
        if (WS_EXEMPT_SUBSTRINGS.any { content.contains(it) }) return@forEach
        API_LITERAL_PATTERN.findAll(content).forEach { match ->
            if (!match.value.startsWith(ALLOWED_API_PREFIX)) {
                violations += "${file.path}:$lineNo: route \"${match.value}\" outside the mobile/v1 BFF " +
                    "(full literal: \"$content\")"
            }
        }
    }

    return violations
}

abstract class CheckBffOnlyNetworkTask : DefaultTask() {

    @get:Internal
    var kotlinSourceFiles: List<File> = emptyList()

    @TaskAction
    fun check() {
        val violations = kotlinSourceFiles
            .filter { it.isFile && it.extension == "kt" }
            .flatMap { findBffOnlyNetworkViolations(it) }
        if (violations.isNotEmpty()) {
            throw GradleException(
                "This module references an /api/* route outside the mobile/v1 BFF or imports " +
                    "okhttp3/retrofit2 directly. Only :data (via :data:mobile-api-client) " +
                    "may do that. Violations:\n" +
                    violations.joinToString("\n") { "  - $it" }
            )
        }
    }
}

class BffOnlyNetworkPlugin : Plugin<Project> {
    override fun apply(project: Project) {
        val sourceTree = project.fileTree(project.file("src/main/kotlin"))
        sourceTree.include("**/*.kt")

        val checkTask = project.tasks.register<CheckBffOnlyNetworkTask>("checkBffOnlyNetwork") {
            group = "verification"
            description = "Fails the build if this module references a non-BFF /api/* route " +
                "or imports okhttp3/retrofit2 directly"
            kotlinSourceFiles = sourceTree.files.toList()
        }
        project.tasks.matching { it.name == "check" }.configureEach {
            dependsOn(checkTask)
        }
    }
}
