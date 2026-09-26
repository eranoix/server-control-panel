package dev.servercontrolpanel.feature.files.transfer

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
 * Renders [TransferScreen] under Robolectric. Only the empty state is covered: transfer rows
 * come only from real `WorkInfo` updates, which have no test seam.
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
        // Built outside setContent so recomposition does not create a new ViewModel.
        val vm = TransferViewModel(application)
        composeRule.setContent {
            TransferScreen(viewModel = vm)
        }
        composeRule.waitForIdle()

        // TransferScreen returns early on an empty map, so nothing should be composed.
        val tree = composeRule.onRoot().printToString()
        assertFalse(tree.contains("Transferência em andamento"))
        assertFalse(tree.contains("Cancelar"))
    }
}
