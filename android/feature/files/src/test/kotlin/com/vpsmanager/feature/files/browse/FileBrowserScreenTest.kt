package com.vpsmanager.feature.files.browse

import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.test.core.app.ApplicationProvider
import androidx.work.Configuration
import androidx.work.testing.SynchronousExecutor
import androidx.work.testing.WorkManagerTestInitHelper
import com.vpsmanager.core.model.FileEntry
import com.vpsmanager.data.files.FileListResult
import com.vpsmanager.data.files.FilesRepository
import com.vpsmanager.feature.files.transfer.TransferViewModel
import kotlinx.coroutines.awaitCancellation
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/**
 * Renders [FileBrowserScreen] under Robolectric in every [FileBrowserUiState]
 * -- never composed before this. Uses the same [BrowserScreenFakeFilesRepository] seam
 * [FileBrowserViewModelTest] already established, but drives the real
 * Compose tree instead of only the state machine.
 */
@RunWith(RobolectricTestRunner::class)
class FileBrowserScreenTest {

    @get:Rule
    val composeRule = createComposeRule()

    private val application = ApplicationProvider.getApplicationContext<android.app.Application>()

    // TransferScreen (rendered inline by FileBrowserScreen whenever
    // pickMode is false) constructs a real TransferViewModel, which reaches
    // for WorkManager.getInstance() eagerly in its init block.
    @Before
    fun setUp() {
        val config = Configuration.Builder().setExecutor(SynchronousExecutor()).build()
        WorkManagerTestInitHelper.initializeTestWorkManager(application, config)
    }

    private fun transferViewModel() = TransferViewModel(application)

    @Test
    fun `loading state shows the progress indicator, never a blank screen`() {
        val repository = BrowserScreenFakeFilesRepository { awaitCancellation() }
        // Construido FORA do setContent: a lambda de conteudo recompoe, e
        // construir la dentro daria um ViewModel novo a cada recomposicao.
        val viewModel = FileBrowserViewModel(repository)
        composeRule.setContent {
            FileBrowserScreen(viewModel = viewModel, transferViewModel = transferViewModel())
        }

        composeRule.onNodeWithText("Loading folder…").assertExists()
    }

    @Test
    fun `error state shows the server's own message and a retry action`() {
        val repository = BrowserScreenFakeFilesRepository { FileListResult.Error("O servidor está indisponível no momento.") }
        // Construido FORA do setContent: a lambda de conteudo recompoe, e
        // construir la dentro daria um ViewModel novo a cada recomposicao.
        val viewModel = FileBrowserViewModel(repository)
        composeRule.setContent {
            FileBrowserScreen(viewModel = viewModel, transferViewModel = transferViewModel())
        }
        composeRule.waitForIdle()

        composeRule.onNodeWithText("O servidor está indisponível no momento.").assertExists()
        composeRule.onNodeWithText("Try again").assertExists()
    }

    @Test
    fun `empty directory renders the empty card, not a stuck spinner`() {
        val repository = BrowserScreenFakeFilesRepository { FileListResult.Empty }
        // Construido FORA do setContent: a lambda de conteudo recompoe, e
        // construir la dentro daria um ViewModel novo a cada recomposicao.
        val viewModel = FileBrowserViewModel(repository)
        composeRule.setContent {
            FileBrowserScreen(viewModel = viewModel, transferViewModel = transferViewModel())
        }
        composeRule.waitForIdle()

        composeRule.onNodeWithText("Empty folder").assertExists()
    }

    @Test
    fun `a directory listing with both files and folders renders every row`() {
        val repository = BrowserScreenFakeFilesRepository {
            FileListResult.Success(
                path = "/srv",
                parent = "/",
                entries = listOf(
                    FileEntry(name = "app", size = 4096, isDir = true, modifiedEpochSeconds = 1),
                    FileEntry(name = "readme.md", size = 0, isDir = false, modifiedEpochSeconds = 2),
                ),
            )
        }
        // Construido FORA do setContent: a lambda de conteudo recompoe, e
        // construir la dentro daria um ViewModel novo a cada recomposicao.
        val viewModel = FileBrowserViewModel(repository)
        composeRule.setContent {
            FileBrowserScreen(viewModel = viewModel, transferViewModel = transferViewModel())
        }
        composeRule.waitForIdle()

        // A zero-byte file is a real, unremarkable case (an empty readme, a
        // touch'd placeholder) -- formatSize's `bytes < 1024` branch must not
        // choke on it.
        composeRule.onNodeWithText("app").assertExists()
        composeRule.onNodeWithText("readme.md").assertExists()
        composeRule.onNodeWithText("0 B").assertExists()
    }
}

private class BrowserScreenFakeFilesRepository(private val onList: suspend (String) -> FileListResult) : FilesRepository() {
    override suspend fun list(path: String): FileListResult = onList(path)
}
