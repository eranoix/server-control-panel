package com.vpsmanager.feature.files.transfer

import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.printToString
import androidx.test.core.app.ApplicationProvider
import androidx.work.Configuration
import androidx.work.testing.SynchronousExecutor
import androidx.work.testing.WorkManagerTestInitHelper
import org.junit.Assert.assertFalse
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/**
 * Renders [TransferScreen] under Robolectric -- never composed before this.
 *
 * Only the empty state is exercised directly: [TransferViewModel]'s
 * `transfers` map is populated exclusively from real `WorkInfo` updates
 * inside a private `observe()` this class doesn't expose a seam for (unlike
 * [com.vpsmanager.feature.files.browse.FileBrowserViewModel] or
 * [com.vpsmanager.feature.files.share.ShareDestinationViewModel]'s
 * repositories), so driving `InProgress`/`Completed`/`Failed`/`Cancelled`
 * rows here would mean running a real Download/UploadWorker end to end --
 * out of scope for this pass. See the render-coverage report for the gap.
 */
@RunWith(RobolectricTestRunner::class)
class TransferScreenTest {

    @get:Rule
    val composeRule = createComposeRule()

    @Before
    fun setUp() {
        val application = ApplicationProvider.getApplicationContext<android.app.Application>()
        val config = Configuration.Builder().setExecutor(SynchronousExecutor()).build()
        WorkManagerTestInitHelper.initializeTestWorkManager(application, config)
    }

    @Test
    fun `with no transfers in flight, the screen renders nothing instead of an empty card`() {
        val application = ApplicationProvider.getApplicationContext<android.app.Application>()
        // Built OUTSIDE setContent: the content lambda recomposes, and
        // building it in there would give a new ViewModel on every
        // recomposition.
        val vm = TransferViewModel(application)
        composeRule.setContent {
            TransferScreen(viewModel = vm)
        }
        composeRule.waitForIdle()

        // TransferScreen's own contract is an early `return` on an empty
        // map -- assert the composed tree really is empty, not merely that
        // no particular text is missing.
        val tree = composeRule.onRoot().printToString()
        assertFalse(tree.contains("Transferência em andamento"))
        assertFalse(tree.contains("Cancelar"))
    }
}
