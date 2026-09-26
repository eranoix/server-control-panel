import org.gradle.api.DefaultTask
import org.gradle.api.GradleException
import org.gradle.api.Plugin
import org.gradle.api.Project
import org.gradle.api.tasks.Internal
import org.gradle.api.tasks.TaskAction
import org.gradle.kotlin.dsl.register
import java.io.File

/**
 * The app's single legal HTTP surface is the api mobile v1 tree via the
 * generated client in :data:mobile-api-client. Every other module reaches the
 * network only through :data's repository abstractions -- never a raw api
 * route outside that tree, never okhttp3/retrofit2 directly. This is the
 * fast, text-based half of that guarantee: it reads Kotlin source lines and
 * flags the two ways a route or a client type shows up in source as plain
 * text.
 *
 * Note for maintainers: Kotlin block comments nest, so this file's doc
 * comments deliberately avoid writing a literal slash-star sequence (the two
 * characters that open a nested comment) anywhere in their prose -- write
 * "api route" instead of the slash-star glob form when editing this file.
 *
 * It does NOT see a route built by concatenation/interpolation (two string
 * pieces joined into one route at runtime) or a fully-qualified reference
 * with no import (okhttp3.OkHttpClient()) -- that bypass class is closed by
 * the detekt rule
 * in :build-logic:lint-rules, which reasons over resolved types instead of
 * source text. The two mechanisms are deliberately independent: this one is a
 * lexical scan of import lines and string-literal contents; the detekt rule
 * never looks at source text at all, only at what the Kotlin compiler
 * resolved the expression to.
 */
private val FORBIDDEN_IMPORT_PREFIXES = listOf(
    "import okhttp3.",
    "import retrofit2.",
)

// Websocket endpoints are a separate, already-approved contract surface (see
// research/ARCHITECTURE.md's "sole legal contract" carve-out) and must never
// trip the /api/* checks below even if a literal happens to mention them.
private val WS_EXEMPT_SUBSTRINGS = listOf("/ws/shell", "/ws/videocall")

private val API_LITERAL_PATTERN = Regex("/api/[\\w./\\-]*")
private const val ALLOWED_API_PREFIX = "/api/mobile/v1"

/**
 * Line-oriented preprocessor that turns a Kotlin source file into two things
 * a naive `grep` cannot separate cleanly:
 *  - `codeOnlyLines`: the file with every line comment and every block
 *    comment (including KDoc) blanked out, line count preserved, so
 *    `import okhttp3.` never matches text that only explains the rule in a
 *    doc comment (this is the exact false-positive class that made an
 *    earlier phase's line-comment-only skip cry wolf on KDoc prose).
 *  - `stringLiterals`: the content of every string literal (regular and
 *    triple-quoted), with the line it started on, so an api route is only
 *    flagged when it is an actual string value, not when the same text
 *    appears inside a comment.
 */
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
                // Char literal -- skip its (possibly escaped) single char so a
                // quote inside it can't be mistaken for a string boundary.
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
            violations += "${file.path}:${index + 1}: import proibido fora do BFF -- $trimmed"
        }
    }

    scan.stringLiterals.forEach { (lineNo, content) ->
        if (WS_EXEMPT_SUBSTRINGS.any { content.contains(it) }) return@forEach
        API_LITERAL_PATTERN.findAll(content).forEach { match ->
            if (!match.value.startsWith(ALLOWED_API_PREFIX)) {
                violations += "${file.path}:$lineNo: rota \"${match.value}\" fora do BFF mobile/v1 " +
                    "(literal completo: \"$content\")"
            }
        }
    }

    return violations
}

abstract class CheckBffOnlyNetworkTask : DefaultTask() {

    // Always re-runs (no up-to-date caching): this is a correctness gate, not
    // a task worth the complexity of incremental input tracking.
    @get:Internal
    var kotlinSourceFiles: List<File> = emptyList()

    @TaskAction
    fun check() {
        val violations = kotlinSourceFiles
            .filter { it.isFile && it.extension == "kt" }
            .flatMap { findBffOnlyNetworkViolations(it) }
        if (violations.isNotEmpty()) {
            throw GradleException(
                "Este modulo referencia uma rota /api/* fora do BFF mobile/v1 ou importa " +
                    "okhttp3/retrofit2 diretamente -- o unico modulo autorizado a isso e " +
                    ":data (via :data:mobile-api-client). Violacoes:\n" +
                    violations.joinToString("\n") { "  - $it" }
            )
        }
    }
}

/**
 * Registers checkBffOnlyNetwork and wires it into the check lifecycle task.
 * Applied by the root build's `subprojects` block to every module outside the
 * BFF exemption set -- never per-module boilerplate, so a module added later
 * is covered automatically without an opt-in step.
 *
 * Applied this early (from the root project's `subprojects` block, before
 * each module's own plugins block has run), the module's own `check` task
 * does not exist yet -- `tasks.named("check")` would fail eagerly. Matching
 * plus configureEach is lazy: it wires the dependency whenever the module's
 * own plugin (AGP or the Kotlin JVM plugin) later registers `check`.
 */
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
