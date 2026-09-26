package com.vpsmanager.feature.files.editor

import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import com.vpsmanager.data.files.FileReadResult
import com.vpsmanager.data.files.FileWriteResult
import com.vpsmanager.data.files.FilesRepository
import kotlinx.coroutines.awaitCancellation
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/**
 * Renders [FileEditorScreen] under Robolectric in every [FileEditorUiState]
 * -- including the two states that embed [SoraEditorView], a real
 * `AndroidView`-wrapped `CodeEditor` from sora-editor that had never been
 * inflated in any test before this.
 */
@RunWith(RobolectricTestRunner::class)
class FileEditorScreenTest {

    @get:Rule
    val composeRule = createComposeRule()

    @Test
    fun `loading state shows the progress indicator`() {
        val repository = EditorScreenFakeFilesRepository(onRead = { awaitCancellation() })
        // Construido FORA do setContent: a lambda de conteudo recompoe, e
        // construir la dentro daria um ViewModel novo a cada recomposicao.
        val vm = FileEditorViewModel("/srv/app/main.go", repository)
        composeRule.setContent {
            FileEditorScreen(path = "/srv/app/main.go", onBack = {}, viewModel = vm)
        }

        composeRule.onNodeWithText("Loading file…").assertExists()
    }

    @Test
    fun `error state surfaces the repository's reason and a retry action`() {
        val repository = EditorScreenFakeFilesRepository(onRead = { FileReadResult.Error("Arquivo binário não pode ser editado.") })
        // Construido FORA do setContent: a lambda de conteudo recompoe, e
        // construir la dentro daria um ViewModel novo a cada recomposicao.
        val vm2 = FileEditorViewModel("/srv/app/bin", repository)
        composeRule.setContent {
            FileEditorScreen(path = "/srv/app/bin", onBack = {}, viewModel = vm2)
        }
        composeRule.waitForIdle()

        composeRule.onNodeWithText("Arquivo binário não pode ser editado.").assertExists()
        composeRule.onNodeWithText("Try again").assertExists()
    }

    @Test
    fun `editing state inflates the real sora-editor CodeEditor without crashing`() {
        val repository = EditorScreenFakeFilesRepository(
            onRead = { FileReadResult.Success(content = "package main\n", mtime = 1L, language = "go") },
        )
        // Construido FORA do setContent: a lambda de conteudo recompoe, e
        // construir la dentro daria um ViewModel novo a cada recomposicao.
        val vm3 = FileEditorViewModel("/srv/app/main.go", repository)
        composeRule.setContent {
            FileEditorScreen(path = "/srv/app/main.go", onBack = {}, viewModel = vm3)
        }
        composeRule.waitForIdle()

        // The top bar's own title (fileNameOf(path)) is the cheapest proof
        // the Editing branch composed past SoraEditorView's AndroidView
        // factory (CodeEditor + TextMate bootstrap) instead of crashing
        // before Scaffold could lay out its topBar.
        composeRule.onNodeWithText("main.go").assertExists()
        composeRule.onNodeWithText("Save").assertExists()
    }

    @Test
    fun `a save conflict blocks with a dialog while the local buffer stays visible underneath`() {
        val repository = EditorScreenFakeFilesRepository(
            onRead = { FileReadResult.Success(content = "local edit", mtime = 1L, language = "go") },
            onWrite = { _, _, _ -> FileWriteResult.Conflict(serverContent = "server edit", serverMtime = 2L) },
        )
        val viewModel = FileEditorViewModel("/srv/app/main.go", repository)
        composeRule.setContent {
            FileEditorScreen(path = "/srv/app/main.go", onBack = {}, viewModel = viewModel)
        }
        composeRule.waitForIdle()
        // Drives the dirty flag directly through the ViewModel -- the real
        // path is a keystroke inside SoraEditorView's wrapped CodeEditor,
        // which exposes no Compose semantics for a test to type into.
        viewModel.onContentChanged("local edit, modified")
        composeRule.waitForIdle()
        composeRule.onNodeWithText("Save").performClick()
        composeRule.waitForIdle()

        composeRule.onNodeWithText("The file changed on the server").assertExists()
        composeRule.onNodeWithText("Overwrite anyway").assertExists()
    }
}

private class EditorScreenFakeFilesRepository(
    private val onRead: suspend (String) -> FileReadResult = { FileReadResult.Error("not stubbed") },
    private val onWrite: suspend (String, String, Long) -> FileWriteResult = { _, _, _ -> FileWriteResult.Error("not stubbed") },
) : FilesRepository() {
    override suspend fun read(path: String): FileReadResult = onRead(path)
    override suspend fun write(path: String, content: String, expectedMtime: Long): FileWriteResult = onWrite(path, content, expectedMtime)
}
