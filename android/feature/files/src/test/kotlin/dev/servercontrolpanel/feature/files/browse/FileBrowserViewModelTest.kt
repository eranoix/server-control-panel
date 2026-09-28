package dev.servercontrolpanel.feature.files.browse

import dev.servercontrolpanel.core.model.FileEntry
import dev.servercontrolpanel.data.files.FileListResult
import dev.servercontrolpanel.data.files.FilesRepository
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Before
import org.junit.Test

private class FakeFilesRepository(private val onList: suspend (String) -> FileListResult) : FilesRepository() {
    override suspend fun list(path: String): FileListResult = onList(path)
}

@OptIn(ExperimentalCoroutinesApi::class)
class FileBrowserViewModelTest {

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
    fun `starts Loading, then reaches Success for the root directory`() = runTest {
        val repository = FakeFilesRepository {
            FileListResult.Success(
                path = "/",
                parent = "/",
                entries = listOf(FileEntry(name = "srv", size = 4096, isDir = true, modifiedEpochSeconds = 1)),
            )
        }
        val viewModel = FileBrowserViewModel(repository)

        assertEquals(FileBrowserUiState.Loading, viewModel.uiState.value)

        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(
            FileBrowserUiState.Success(
                currentPath = "/",
                entries = listOf(FileEntry(name = "srv", size = 4096, isDir = true, modifiedEpochSeconds = 1)),
            ),
            viewModel.uiState.value,
        )
    }

    @Test
    fun `reaches Empty for a directory with zero entries`() = runTest {
        val repository = FakeFilesRepository { FileListResult.Empty }
        val viewModel = FileBrowserViewModel(repository)

        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(FileBrowserUiState.Empty, viewModel.uiState.value)
    }

    @Test
    fun `reaches Error when the repository reports a failure`() = runTest {
        val repository = FakeFilesRepository { FileListResult.Error("The server is unavailable right now.") }
        val viewModel = FileBrowserViewModel(repository)

        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(
            FileBrowserUiState.Error("The server is unavailable right now."),
            viewModel.uiState.value,
        )
    }

    @Test
    fun `navigateInto a directory re-fetches and moves the current path forward`() = runTest {
        val childEntry = FileEntry(name = "app", size = 0, isDir = true, modifiedEpochSeconds = 2)
        val repository = FakeFilesRepository { path ->
            when (path) {
                "/" -> FileListResult.Success(path = "/", parent = "/", entries = listOf(childEntry))
                "/app" -> FileListResult.Empty
                else -> FileListResult.Error("unexpected path: $path")
            }
        }
        val viewModel = FileBrowserViewModel(repository)
        dispatcher.scheduler.advanceUntilIdle()

        viewModel.navigateInto(childEntry)

        assertEquals(FileBrowserUiState.Loading, viewModel.uiState.value)

        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(FileBrowserUiState.Empty, viewModel.uiState.value)
        assertEquals("/app", viewModel.currentPath)
    }

    @Test
    fun `navigateUp returns to the parent directory`() = runTest {
        val childEntry = FileEntry(name = "app", size = 0, isDir = true, modifiedEpochSeconds = 2)
        val repository = FakeFilesRepository { path ->
            when (path) {
                "/" -> FileListResult.Success(path = "/", parent = "/", entries = listOf(childEntry))
                "/app" -> FileListResult.Empty
                else -> FileListResult.Error("unexpected path: $path")
            }
        }
        val viewModel = FileBrowserViewModel(repository)
        dispatcher.scheduler.advanceUntilIdle()
        viewModel.navigateInto(childEntry)
        dispatcher.scheduler.advanceUntilIdle()

        viewModel.navigateUp()
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals("/", viewModel.currentPath)
        assertEquals(
            FileBrowserUiState.Success(currentPath = "/", entries = listOf(childEntry)),
            viewModel.uiState.value,
        )
    }
}
