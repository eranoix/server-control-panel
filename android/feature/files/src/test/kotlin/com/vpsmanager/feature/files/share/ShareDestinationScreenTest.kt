package com.vpsmanager.feature.files.share

import android.app.Application
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.test.core.app.ApplicationProvider
import androidx.work.Configuration
import androidx.work.testing.SynchronousExecutor
import androidx.work.testing.WorkManagerTestInitHelper
import com.vpsmanager.data.files.FilesRepository
import com.vpsmanager.data.files.InboxDirResult
import com.vpsmanager.feature.files.transfer.TransferViewModel
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/**
 * Renders [ShareDestinationScreen] under Robolectric — this screen (and
 * every other Compose screen in the app) had never actually been composed
 * before, on a device or in a test. This is the render test that would have
 * caught the fix that avoids a name collision when sharing several items at
 * once: `ACTION_SEND_MULTIPLE` sharing two items that both resolve to the
 * same `displayName` (routine — two photos with no distinguishing
 * `DISPLAY_NAME` column both fall back to the literal `"arquivo"`, see
 * `ShareTargetActivity.resolveSharedUri`) used to blow up
 * `LazyColumn(key = { it.displayName })` with `IllegalArgumentException: Key
 * ... was already used`.
 *
 * Both [ShareDestinationViewModel] and [TransferViewModel] are constructed
 * directly (never via the `viewModel()` factory default) so this test never
 * touches a real network client beyond what [FakeFilesRepository] fakes at
 * the seam `FileBrowserViewModelTest` already established, and WorkManager
 * runs against [WorkManagerTestInitHelper]'s synchronous test instance
 * instead of a real background executor.
 */
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

    /** The regression: duplicate `displayName`s across shared items must not crash the list. */
    @Test
    fun `two items sharing the same display name render without crashing`() {
        val duplicateNamedItems = listOf(
            SharedItem(uri = "content://provider/1", displayName = "arquivo", sizeBytes = 100),
            SharedItem(uri = "content://provider/2", displayName = "arquivo", sizeBytes = 200),
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

        // disambiguateSharedItems must have renamed the second occurrence --
        // both original and disambiguated names are visible, proving the
        // list actually rendered both rows instead of throwing before either
        // one reached the screen.
        composeRule.onNodeWithText("arquivo").assertExists()
        composeRule.onNodeWithText("arquivo (2)").assertExists()
    }

    @Test
    fun `a duplicate name with an extension gets suffixed before the extension, not after`() {
        val items = listOf(
            SharedItem(uri = "content://provider/1", displayName = "foto.jpg", sizeBytes = 100),
            SharedItem(uri = "content://provider/2", displayName = "foto.jpg", sizeBytes = 200),
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

        composeRule.onNodeWithText("foto.jpg").assertExists()
        composeRule.onNodeWithText("foto (2).jpg").assertExists()
    }

    @Test
    fun `an inbox resolution error is shown instead of a silent no-op`() {
        val viewModel = ShareDestinationViewModel(
            application,
            filesRepository = FakeFilesRepository(
                onInboxPath = { InboxDirResult.Error("O servidor está indisponível no momento.") },
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

        composeRule.onNodeWithText("O servidor está indisponível no momento.").assertExists()
    }
}

/**
 * A fake at the [FilesRepository] seam -- mirrors
 * [com.vpsmanager.feature.files.browse.FileBrowserViewModelTest]'s
 * `FakeFilesRepository` precedent.
 */
private class FakeFilesRepository(
    private val onInboxPath: suspend () -> InboxDirResult = { InboxDirResult.Success("/srv/inbox") },
) : FilesRepository() {
    override suspend fun inboxPath(): InboxDirResult = onInboxPath()
}
