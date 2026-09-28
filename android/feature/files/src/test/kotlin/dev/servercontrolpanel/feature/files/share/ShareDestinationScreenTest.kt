package dev.servercontrolpanel.feature.files.share

import android.app.Application
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.test.core.app.ApplicationProvider
import androidx.work.Configuration
import androidx.work.testing.SynchronousExecutor
import androidx.work.testing.WorkManagerTestInitHelper
import dev.servercontrolpanel.data.files.FilesRepository
import dev.servercontrolpanel.data.files.InboxDirResult
import dev.servercontrolpanel.feature.files.transfer.TransferViewModel
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

@RunWith(RobolectricTestRunner::class)
class ShareDestinationScreenTest {

    @get:Rule
    val composeRule = createComposeRule()

    private lateinit var application: Application

    @Before
    fun setUp() {
        application = ApplicationProvider.getApplicationContext()
        val config = Configuration.Builder().setExecutor(SynchronousExecutor()).build()
        WorkManagerTestInitHelper.initializeTestWorkManager(application, config)
    }

    @Test
    fun `two items sharing the same display name render without crashing`() {
        val duplicateNamedItems = listOf(
            SharedItem(uri = "content://provider/1", displayName = "file", sizeBytes = 100),
            SharedItem(uri = "content://provider/2", displayName = "file", sizeBytes = 200),
        )
        val viewModel = ShareDestinationViewModel(application, filesRepository = FakeFilesRepository())
        val transferViewModel = TransferViewModel(application)

        composeRule.setContent {
            ShareDestinationScreen(
                sharedItems = duplicateNamedItems,
                onDone = {},
                viewModel = viewModel,
                transferViewModel = transferViewModel,
            )
        }

        composeRule.onNodeWithText("file").assertExists()
        composeRule.onNodeWithText("file (2)").assertExists()
    }

    @Test
    fun `a duplicate name with an extension gets suffixed before the extension, not after`() {
        val items = listOf(
            SharedItem(uri = "content://provider/1", displayName = "photo.jpg", sizeBytes = 100),
            SharedItem(uri = "content://provider/2", displayName = "photo.jpg", sizeBytes = 200),
        )
        val viewModel = ShareDestinationViewModel(application, filesRepository = FakeFilesRepository())
        val transferViewModel = TransferViewModel(application)

        composeRule.setContent {
            ShareDestinationScreen(
                sharedItems = items,
                onDone = {},
                viewModel = viewModel,
                transferViewModel = transferViewModel,
            )
        }

        composeRule.onNodeWithText("photo.jpg").assertExists()
        composeRule.onNodeWithText("photo (2).jpg").assertExists()
    }

    @Test
    fun `an inbox resolution error is shown instead of a silent no-op`() {
        val viewModel = ShareDestinationViewModel(
            application,
            filesRepository = FakeFilesRepository(
                onInboxPath = { InboxDirResult.Error("The server is unavailable right now.") },
            ),
        )
        val transferViewModel = TransferViewModel(application)

        composeRule.setContent {
            ShareDestinationScreen(
                sharedItems = listOf(SharedItem(uri = "content://provider/1", displayName = "a.txt", sizeBytes = 1)),
                onDone = {},
                viewModel = viewModel,
                transferViewModel = transferViewModel,
            )
        }

        composeRule.onNodeWithText("Upload to the default folder").performClick()

        composeRule.onNodeWithText("The server is unavailable right now.").assertExists()
    }
}

private class FakeFilesRepository(
    private val onInboxPath: suspend () -> InboxDirResult = { InboxDirResult.Success("/srv/inbox") },
) : FilesRepository() {
    override suspend fun inboxPath(): InboxDirResult = onInboxPath()
}
