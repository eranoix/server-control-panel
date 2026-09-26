package com.vpsmanager.feature.files.editor

import android.content.Context
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.viewinterop.AndroidView
import io.github.rosemoe.sora.event.ContentChangeEvent
import io.github.rosemoe.sora.lang.EmptyLanguage
import io.github.rosemoe.sora.langs.textmate.TextMateColorScheme
import io.github.rosemoe.sora.langs.textmate.TextMateLanguage
import io.github.rosemoe.sora.langs.textmate.registry.FileProviderRegistry
import io.github.rosemoe.sora.langs.textmate.registry.GrammarRegistry
import io.github.rosemoe.sora.langs.textmate.registry.ThemeRegistry
import io.github.rosemoe.sora.langs.textmate.registry.dsl.languages
import io.github.rosemoe.sora.langs.textmate.registry.model.ThemeModel
import io.github.rosemoe.sora.langs.textmate.registry.provider.AssetsFileResolver
import io.github.rosemoe.sora.widget.CodeEditor
import io.github.rosemoe.sora.widget.subscribeAlways
import org.eclipse.tm4e.core.registry.IThemeSource

/**
 * One-time, process-wide bootstrap of sora-editor's TextMate registries
 * (theme + grammars, read from this module's bundled `assets/textmate`
 * files). The registries are static singletons inside sora-editor itself,
 * so this must run exactly once regardless of how many [SoraEditorView]
 * instances get composed/disposed across navigation.
 *
 * The bundled theme and grammars are original, minimal TextMate assets
 * authored for this project -- not copied from sora-editor's sample app or
 * any other third party -- so they carry no license obligation beyond this
 * project's own. sora-editor's LGPL-2.1 terms cover only the library code
 * itself (see OssLicensesScreen), which stays unmodified and Maven-linked.
 */
private object TextMateSetup {
    @Volatile
    private var initialized = false

    /**
     * Maps the language string [com.vpsmanager.data.files.FilesRepository]
     * reports (mirroring the backend's `internal/files/mobile_adapter.go`
     * extension-to-language map) to the TextMate scope name registered
     * below. A language with no bundled grammar returns null; the caller
     * falls back to [EmptyLanguage] rather than guessing a scope name.
     */
    private val scopeNameByLanguage = mapOf(
        "go" to "source.go",
        "python" to "source.python",
        "javascript" to "source.js",
        "json" to "source.json",
        "yaml" to "source.yaml",
        "shell" to "source.shell",
        "markdown" to "text.markdown",
    )

    fun scopeNameFor(language: String): String? = scopeNameByLanguage[language]

    @Synchronized
    fun ensureInitialized(context: Context) {
        if (initialized) return
        val appContext = context.applicationContext
        FileProviderRegistry.getInstance().addFileProvider(AssetsFileResolver(appContext.assets))

        val themeRegistry = ThemeRegistry.getInstance()
        val themePath = "textmate/theme.json"
        themeRegistry.loadTheme(
            ThemeModel(
                IThemeSource.fromInputStream(
                    FileProviderRegistry.getInstance().tryGetInputStream(themePath),
                    themePath,
                    null,
                ),
                "vpsmanager-dark",
            ).apply { isDark = true },
        )
        themeRegistry.setTheme("vpsmanager-dark")

        GrammarRegistry.getInstance().loadGrammars(
            languages {
                language("go") {
                    grammar = "textmate/go/syntaxes/go.tmLanguage.json"
                    scopeName = "source.go"
                    languageConfiguration = "textmate/go/language-configuration.json"
                }
                language("python") {
                    grammar = "textmate/python/syntaxes/python.tmLanguage.json"
                    scopeName = "source.python"
                    languageConfiguration = "textmate/python/language-configuration.json"
                }
                language("javascript") {
                    grammar = "textmate/javascript/syntaxes/javascript.tmLanguage.json"
                    scopeName = "source.js"
                    languageConfiguration = "textmate/javascript/language-configuration.json"
                }
                language("json") {
                    grammar = "textmate/json/syntaxes/json.tmLanguage.json"
                    scopeName = "source.json"
                    languageConfiguration = "textmate/json/language-configuration.json"
                }
                language("yaml") {
                    grammar = "textmate/yaml/syntaxes/yaml.tmLanguage.json"
                    scopeName = "source.yaml"
                    languageConfiguration = "textmate/yaml/language-configuration.json"
                }
                language("shell") {
                    grammar = "textmate/shell/syntaxes/shell.tmLanguage.json"
                    scopeName = "source.shell"
                    languageConfiguration = "textmate/shell/language-configuration.json"
                }
                language("markdown") {
                    grammar = "textmate/markdown/syntaxes/markdown.tmLanguage.json"
                    scopeName = "text.markdown"
                    languageConfiguration = "textmate/markdown/language-configuration.json"
                }
            },
        )

        initialized = true
    }
}

/**
 * Compose interop wrapper around sora-editor's View-based [CodeEditor].
 * Configures TextMate syntax highlighting for [language] (the string
 * `FilesRepository.read()` reports, e.g. "go"/"python"/"json" -- see
 * [TextMateSetup.scopeNameFor]), and forwards local edits back to the
 * caller via [onContentChanged] on every content-change event. The editor
 * keeps its own text buffer; only the dirty flag/content mirror on the
 * Kotlin side updates per keystroke, not the whole Compose screen.
 */
@Composable
fun SoraEditorView(
    content: String,
    language: String,
    onContentChanged: (String) -> Unit,
    modifier: Modifier = Modifier,
) {
    // Tracks the content this Composable itself last pushed into the
    // editor (initial load or a conflict-reload), so `update` can tell an
    // externally driven content change (reload) apart from the editor's
    // own local edits and avoid clobbering the buffer mid-typing.
    var lastAppliedContent by remember { mutableStateOf(content) }

    AndroidView(
        modifier = modifier.fillMaxSize(),
        factory = { ctx ->
            TextMateSetup.ensureInitialized(ctx)
            CodeEditor(ctx).apply {
                setText(content, null)
                setEditorLanguage(languageFor(language))
                colorScheme = TextMateColorScheme.create(ThemeRegistry.getInstance())
                subscribeAlways<ContentChangeEvent> {
                    val current = text.toString()
                    lastAppliedContent = current
                    onContentChanged(current)
                }
            }
        },
        update = { editor ->
            if (content != lastAppliedContent && content != editor.text.toString()) {
                lastAppliedContent = content
                editor.setText(content, null)
            }
        },
        onRelease = { editor -> editor.release() },
    )
}

private fun languageFor(language: String) =
    TextMateSetup.scopeNameFor(language)?.let { scopeName -> TextMateLanguage.create(scopeName, true) }
        ?: EmptyLanguage()
