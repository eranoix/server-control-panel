package dev.servercontrolpanel.feature.admin.ops

import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import dev.servercontrolpanel.data.ops.OpsAlert
import dev.servercontrolpanel.data.ops.OpsSnapshot
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

@RunWith(RobolectricTestRunner::class)
class OpsDashboardScreenTest {

    @get:Rule
    val composeRule = createComposeRule()

    @Test
    fun `loading state shows the progress indicator`() {
        composeRule.setContent {
            OpsDashboardScreen(uiState = OpsDashboardUiState.Loading, onBack = {}, onRetry = {})
        }

        composeRule.onNodeWithText("Operations dashboard").assertExists()
    }

    @Test
    fun `error state surfaces the reason and offers retry`() {
        var retried = false
        composeRule.setContent {
            OpsDashboardScreen(
                uiState = OpsDashboardUiState.LoadError("The server is unavailable right now."),
                onBack = {},
                onRetry = { retried = true },
            )
        }

        composeRule.onNodeWithText("The server is unavailable right now.").assertExists()
        composeRule.onNodeWithText("Try again").performClick()
        assert(retried) { "expected onRetry to fire" }
    }

    @Test
    fun `an empty ops snapshot renders every section without a stale-looking blank alert list`() {
        val snapshot = OpsSnapshot(
            health = mapOf("docker" to "ok", "whatsapp" to "connected"),
            healthOk = true,
            queueRunning = 0,
            queueQueued = 0,
            alerts = emptyList(),
        )
        composeRule.setContent {
            OpsDashboardScreen(uiState = OpsDashboardUiState.Success(snapshot), onBack = {}, onRetry = {})
        }

        composeRule.onNodeWithText("Health checks (all ok)").assertExists()
        composeRule.onNodeWithText("docker").assertExists()

        composeRule.onNodeWithText("Active alerts (0)").assertExists()
        composeRule.onNodeWithText("No alerts firing right now.").assertDoesNotExist()
        composeRule.onNodeWithText("Active alerts (0)").performClick()
        composeRule.onNodeWithText("No alerts firing right now.").assertExists()
    }

    @Test
    fun `a snapshot with a firing alert expands the alerts section by default and shows its values`() {
        val snapshot = OpsSnapshot(
            health = mapOf("disk" to "critical"),
            healthOk = false,
            queueRunning = 2,
            queueQueued = 5,
            alerts = listOf(
                OpsAlert(
                    name = "disk_usage",
                    severity = "critical",
                    state = "firing",
                    currentValue = 95.0,
                    threshold = 90.0,
                    unit = "%",
                ),
            ),
        )
        composeRule.setContent {
            OpsDashboardScreen(uiState = OpsDashboardUiState.Success(snapshot), onBack = {}, onRetry = {})
        }

        composeRule.onNodeWithText("Health checks (failures)").assertExists()
        composeRule.onNodeWithText("Queue (2 running, 5 waiting)").assertExists()
        composeRule.onNodeWithText("Active alerts (1)").assertExists()
        composeRule.onNodeWithText("disk_usage — critical (firing)").assertExists()
        composeRule.onNodeWithText("Current value: 95.0% · threshold: 90.0%").assertExists()
    }
}
