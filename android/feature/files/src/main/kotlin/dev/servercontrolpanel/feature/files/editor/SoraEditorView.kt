package dev.servercontrolpanel.feature.files.editor

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

private object TextMateSetup {
    @Volatile
    private var initialized = false

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
                "servercontrolpanel-dark",
            ).apply { isDark = true },
        )
        themeRegistry.setTheme("servercontrolpanel-dark")

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

@Composable
fun SoraEditorView(
    content: String,
    language: String,
    onContentChanged: (String) -> Unit,
    modifier: Modifier = Modifier,
) {
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
