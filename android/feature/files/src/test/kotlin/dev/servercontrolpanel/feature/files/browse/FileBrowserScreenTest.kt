package dev.servercontrolpanel.feature.files.browse

import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.test.core.app.ApplicationProvider
import androidx.work.Configuration
import androidx.work.testing.SynchronousExecutor
import androidx.work.testing.WorkManagerTestInitHelper
import dev.servercontrolpanel.core.model.FileEntry
import dev.servercontrolpanel.data.files.FileListResult
import dev.servercontrolpanel.data.files.FilesRepository
import dev.servercontrolpanel.feature.files.transfer.TransferViewModel
import kotlinx.coroutines.awaitCancellation
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

@RunWith(RobolectricTestRunner::class)
class FileBrowserScreenTest {

    @get:Rule
    val composeRule = createComposeRule()

    private val application = ApplicationProvider.getApplicationContext<android.app.Application>()

    @Before
    fun setUp() {
        val config = Configuration.Builder().setExecutor(SynchronousExecutor()).build()
        WorkManagerTestInitHelper.initializeTestWorkManager(application, config)
    }

    private fun transferViewModel() = TransferViewModel(application)

    @Test
    fun `loading state shows the progress indicator, never a blank screen`() {
        val repository = BrowserScreenFakeFilesRepository { awaitCancellation() }
        val viewModel = FileBrowserViewModel(repository)
        composeRule.setContent {
            FileBrowserScreen(viewModel = viewModel, transferViewModel = transferViewModel())
        }

        composeRule.onNodeWithText("Loading folder…").assertExists()
    }

    @Test
    fun `error state shows the server's own message and a retry action`() {
        val repository = BrowserScreenFakeFilesRepository { FileListResult.Error("The server is unavailable right now.") }
        val viewModel = FileBrowserViewModel(repository)
        composeRule.setContent {
            FileBrowserScreen(viewModel = viewModel, transferViewModel = transferViewModel())
        }
        composeRule.waitForIdle()

        composeRule.onNodeWithText("The server is unavailable right now.").assertExists()
        composeRule.onNodeWithText("Try again").assertExists()
    }

    @Test
    fun `empty directory renders the empty card, not a stuck spinner`() {
        val repository = BrowserScreenFakeFilesRepository { FileListResult.Empty }
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
        val viewModel = FileBrowserViewModel(repository)
        composeRule.setContent {
            FileBrowserScreen(viewModel = viewModel, transferViewModel = transferViewModel())
        }
        composeRule.waitForIdle()

        composeRule.onNodeWithText("app").assertExists()
        composeRule.onNodeWithText("readme.md").assertExists()
        composeRule.onNodeWithText("0 B").assertExists()
    }
}

private class BrowserScreenFakeFilesRepository(private val onList: suspend (String) -> FileListResult) : FilesRepository() {
    override suspend fun list(path: String): FileListResult = onList(path)
}
