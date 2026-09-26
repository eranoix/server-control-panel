package com.vpsmanager.feature.files.browse

import com.vpsmanager.core.model.FileEntry
import com.vpsmanager.data.files.FileListResult
import com.vpsmanager.data.files.FilesRepository
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

/**
 * A fake at the [FilesRepository] seam -- never touches the generated
 * mobile-api-client (that mapping is [com.vpsmanager.data.files.FilesRepositoryTest]'s
 * job against a real `MockWebServer`); this only exercises the ViewModel's
 * own state machine given a repository outcome.
 */
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
        val repository = FakeFilesRepository { FileListResult.Error("O servidor está indisponível no momento.") }
        val viewModel = FileBrowserViewModel(repository)

        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(
            FileBrowserUiState.Error("O servidor está indisponível no momento."),
            viewModel.uiState.value,
        )
    }

    @Test
    fun `navigateInto a directory re-fetches and moves the current path forward`() = runTest {
        val childEntry = FileEntry(name = "app", size = 0, isDir = true, modifiedEpochSeconds = 2)
        val repository = FakeFilesRepository { path ->
            when (path) {
                "/" -> FileListResult.Success(path = "/", parent = "/", entries = listOf(childEntry))
                // Empty, not Success(entries = emptyList()) -- the real FilesRepository never
                // emits the latter (that shape is exactly what Empty replaces), so the fake
                // has to preserve the same contract for the ViewModel test to mean anything.
                "/app" -> FileListResult.Empty
                else -> FileListResult.Error("caminho inesperado: $path")
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
                else -> FileListResult.Error("caminho inesperado: $path")
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
