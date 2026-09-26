package com.vpsmanager.feature.files.editor

import com.vpsmanager.data.files.FileReadResult
import com.vpsmanager.data.files.FileWriteResult
import com.vpsmanager.data.files.FilesRepository
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

/**
 * A fake at the [FilesRepository] seam -- mirrors
 * [com.vpsmanager.feature.files.browse.FileBrowserViewModelTest]'s
 * `FakeFilesRepository` precedent. Never touches the generated
 * mobile-api-client; that mapping is `FilesRepositoryTest`'s job against a
 * real `MockWebServer`. This only exercises the ViewModel's own state
 * machine given repository outcomes.
 */
private class FakeFilesRepository(
    private val onRead: suspend (String) -> FileReadResult = { FileReadResult.Error("not stubbed") },
    private val onWrite: suspend (String, String, Long) -> FileWriteResult = { _, _, _ -> FileWriteResult.Error("not stubbed") },
) : FilesRepository() {
    var writeCalls = mutableListOf<Triple<String, String, Long>>()
        private set

    override suspend fun read(path: String): FileReadResult = onRead(path)

    override suspend fun write(path: String, content: String, expectedMtime: Long): FileWriteResult {
        writeCalls.add(Triple(path, content, expectedMtime))
        return onWrite(path, content, expectedMtime)
    }
}

@OptIn(ExperimentalCoroutinesApi::class)
class FileEditorViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @Before
    fun setUp() {
        Dispatchers.setMain(dispatcher)
    }

    @After
    fun tearDown() {
        Dispatchers.resetMain()
    }

    @Test
    fun `starts Loading, then reaches Editing for a successful read`() = runTest {
        val repository = FakeFilesRepository(
            onRead = { FileReadResult.Success(content = "package main\n", mtime = 100, language = "go") },
        )
        val viewModel = FileEditorViewModel("/srv/main.go", repository)

        assertEquals(FileEditorUiState.Loading, viewModel.uiState.value)

        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(
            FileEditorUiState.Editing(
                path = "/srv/main.go",
                content = "package main\n",
                mtime = 100,
                language = "go",
                isDirty = false,
            ),
            viewModel.uiState.value,
        )
    }

    @Test
    fun `reaches Error when the repository reports a read failure`() = runTest {
        val repository = FakeFilesRepository(onRead = { FileReadResult.Error("Arquivo não encontrado.") })
        val viewModel = FileEditorViewModel("/srv/gone.go", repository)

        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(FileEditorUiState.Error("Arquivo não encontrado."), viewModel.uiState.value)
    }

    @Test
    fun `onContentChanged marks the buffer dirty without calling save`() = runTest {
        val repository = FakeFilesRepository(
            onRead = { FileReadResult.Success(content = "a", mtime = 100, language = "plaintext") },
        )
        val viewModel = FileEditorViewModel("/srv/f.txt", repository)
        dispatcher.scheduler.advanceUntilIdle()

        viewModel.onContentChanged("ab")

        val state = viewModel.uiState.value as FileEditorUiState.Editing
        assertEquals("ab", state.content)
        assertTrue(state.isDirty)
        assertTrue(repository.writeCalls.isEmpty())
    }

    @Test
    fun `save transitions Editing to Saving then back to Editing with the new mtime on success`() = runTest {
        val repository = FakeFilesRepository(
            onRead = { FileReadResult.Success(content = "a", mtime = 100, language = "plaintext") },
            onWrite = { _, _, _ -> FileWriteResult.Success(mtime = 200) },
        )
        val viewModel = FileEditorViewModel("/srv/f.txt", repository)
        dispatcher.scheduler.advanceUntilIdle()
        viewModel.onContentChanged("ab")

        viewModel.save()

        assertTrue(viewModel.uiState.value is FileEditorUiState.Saving)

        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(
            FileEditorUiState.Editing(
                path = "/srv/f.txt",
                content = "ab",
                mtime = 200,
                language = "plaintext",
                isDirty = false,
            ),
            viewModel.uiState.value,
        )
        assertEquals(listOf(Triple("/srv/f.txt", "ab", 100L)), repository.writeCalls)
    }

    @Test
    fun `save is a no-op while the buffer is not dirty`() = runTest {
        val repository = FakeFilesRepository(
            onRead = { FileReadResult.Success(content = "a", mtime = 100, language = "plaintext") },
        )
        val viewModel = FileEditorViewModel("/srv/f.txt", repository)
        dispatcher.scheduler.advanceUntilIdle()

        viewModel.save()
        dispatcher.scheduler.advanceUntilIdle()

        assertTrue(repository.writeCalls.isEmpty())
        assertTrue((viewModel.uiState.value as FileEditorUiState.Editing).isDirty.not())
    }

    @Test
    fun `a write conflict produces the Conflict state with both local and server content`() = runTest {
        val repository = FakeFilesRepository(
            onRead = { FileReadResult.Success(content = "local", mtime = 100, language = "plaintext") },
            onWrite = { _, _, _ ->
                FileWriteResult.Conflict(serverContent = "server version", serverMtime = 150)
            },
        )
        val viewModel = FileEditorViewModel("/srv/f.txt", repository)
        dispatcher.scheduler.advanceUntilIdle()
        viewModel.onContentChanged("local edit")

        viewModel.save()
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(
            FileEditorUiState.Conflict(
                path = "/srv/f.txt",
                localContent = "local edit",
                localMtime = 100,
                serverContent = "server version",
                serverMtime = 150,
                language = "plaintext",
            ),
            viewModel.uiState.value,
        )
    }

    @Test
    fun `resolveReload discards the local edit and re-enters Editing with the server's content`() = runTest {
        val repository = FakeFilesRepository(
            onRead = { FileReadResult.Success(content = "local", mtime = 100, language = "plaintext") },
            onWrite = { _, _, _ -> FileWriteResult.Conflict(serverContent = "server version", serverMtime = 150) },
        )
        val viewModel = FileEditorViewModel("/srv/f.txt", repository)
        dispatcher.scheduler.advanceUntilIdle()
        viewModel.onContentChanged("local edit")
        viewModel.save()
        dispatcher.scheduler.advanceUntilIdle()

        viewModel.resolveReload()

        assertEquals(
            FileEditorUiState.Editing(
                path = "/srv/f.txt",
                content = "server version",
                mtime = 150,
                language = "plaintext",
                isDirty = false,
            ),
            viewModel.uiState.value,
        )
    }

    @Test
    fun `resolveOverwrite re-issues write with the server's mtime and returns to Editing on success`() = runTest {
        var callCount = 0
        val repository = FakeFilesRepository(
            onRead = { FileReadResult.Success(content = "local", mtime = 100, language = "plaintext") },
            onWrite = { _, _, expectedMtime ->
                callCount++
                if (callCount == 1) {
                    FileWriteResult.Conflict(serverContent = "server version", serverMtime = 150)
                } else {
                    assertEquals(150L, expectedMtime)
                    FileWriteResult.Success(mtime = 151)
                }
            },
        )
        val viewModel = FileEditorViewModel("/srv/f.txt", repository)
        dispatcher.scheduler.advanceUntilIdle()
        viewModel.onContentChanged("local edit")
        viewModel.save()
        dispatcher.scheduler.advanceUntilIdle()

        viewModel.resolveOverwrite()
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(2, repository.writeCalls.size)
        assertEquals(Triple("/srv/f.txt", "local edit", 150L), repository.writeCalls[1])
        assertEquals(
            FileEditorUiState.Editing(
                path = "/srv/f.txt",
                content = "local edit",
                mtime = 151,
                language = "plaintext",
                isDirty = false,
            ),
            viewModel.uiState.value,
        )
    }

    @Test
    fun `resolveCancel keeps the local edit untouched and restores the stale mtime`() = runTest {
        val repository = FakeFilesRepository(
            onRead = { FileReadResult.Success(content = "local", mtime = 100, language = "plaintext") },
            onWrite = { _, _, _ -> FileWriteResult.Conflict(serverContent = "server version", serverMtime = 150) },
        )
        val viewModel = FileEditorViewModel("/srv/f.txt", repository)
        dispatcher.scheduler.advanceUntilIdle()
        viewModel.onContentChanged("local edit")
        viewModel.save()
        dispatcher.scheduler.advanceUntilIdle()

        viewModel.resolveCancel()

        assertEquals(
            FileEditorUiState.Editing(
                path = "/srv/f.txt",
                content = "local edit",
                mtime = 100,
                language = "plaintext",
                isDirty = true,
            ),
            viewModel.uiState.value,
        )
    }
}
