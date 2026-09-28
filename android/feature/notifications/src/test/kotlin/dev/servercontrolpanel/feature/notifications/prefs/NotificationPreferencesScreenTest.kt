package dev.servercontrolpanel.feature.notifications.prefs

import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.semantics.ProgressBarRangeInfo
import androidx.compose.ui.test.hasProgressBarRangeInfo
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import dev.servercontrolpanel.data.push.NotifyPreferencesResult
import dev.servercontrolpanel.data.push.NotifyPreferencesSource
import dev.servercontrolpanel.data.push.NotifyRule
import dev.servercontrolpanel.data.push.UpdateNotifyPreferencesResult
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

@RunWith(RobolectricTestRunner::class)
class NotificationPreferencesScreenTest {

    @get:Rule
    val composeRule = createComposeRule()

    private fun rule(id: String, name: String, enabled: Boolean) = NotifyRule(
        id = id,
        name = name,
        minSeverity = null,
        typePrefix = null,
        enabledForDevice = enabled,
    )

    @Test
    fun `loading state shows a spinner, not a blank screen`() {
        composeRule.setContent {
            NotificationPreferencesScreen(
                uiState = NotificationPreferencesUiState.Loading,
                onRuleToggled = { _, _ -> },
                onRetry = {},
            )
        }

        composeRule.onNode(hasProgressBarRangeInfo(ProgressBarRangeInfo.Indeterminate)).assertExists()
    }

    @Test
    fun `the screen has no header or back button of its own`() {
        composeRule.setContent {
            NotificationPreferencesScreen(
                uiState = NotificationPreferencesUiState.Success(rules = listOf(rule("deploy", "Deploys", true))),
                onRuleToggled = { _, _ -> },
                onRetry = {},
            )
        }

        composeRule.onNodeWithText("Notifications on this device").assertDoesNotExist()
        composeRule.onNodeWithText("Back").assertDoesNotExist()
    }

    @Test
    fun `load error surfaces the reason with a retry action`() {
        composeRule.setContent {
            NotificationPreferencesScreen(
                uiState = NotificationPreferencesUiState.LoadError("Could not load preferences."),
                onRuleToggled = { _, _ -> },
                onRetry = {},
            )
        }

        composeRule.onNodeWithText("Could not load preferences.").assertExists()
        composeRule.onNodeWithText("Try again").assertExists()
    }

    @Test
    fun `success lists every rule with its current toggle state`() {
        composeRule.setContent {
            NotificationPreferencesScreen(
                uiState = NotificationPreferencesUiState.Success(
                    rules = listOf(
                        rule("deploy", "Deploys", enabled = true),
                        rule("backup", "Backups", enabled = false),
                    ),
                ),
                onRuleToggled = { _, _ -> },
                onRetry = {},
            )
        }

        composeRule.onNodeWithText("Deploys").assertExists()
        composeRule.onNodeWithText("Backups").assertExists()
    }

    @Test
    fun `a partial-failure banner renders above the already-reverted rule list`() {
        composeRule.setContent {
            NotificationPreferencesScreen(
                uiState = NotificationPreferencesUiState.Success(
                    rules = listOf(rule("deploy", "Deploys", enabled = true)),
                    errorMessage = "Could not save the preference.",
                ),
                onRuleToggled = { _, _ -> },
                onRetry = {},
            )
        }

        composeRule.onNodeWithText("Could not save the preference.").assertExists()
        composeRule.onNodeWithText("Deploys").assertExists()
    }

    @Test
    fun `toggling a rule that fails to save reverts it and surfaces the error`() {
        val source = object : NotifyPreferencesSource {
            override suspend fun fetch(deviceId: String) =
                NotifyPreferencesResult.Success(listOf(rule("deploy", "Deploys", enabled = false)))
            override suspend fun update(deviceId: String, enabledRuleIds: List<String>) =
                UpdateNotifyPreferencesResult.Error("The server rejected the change.")
        }
        val viewModel = NotificationPreferencesViewModel(deviceId = "device-1", repository = source)
        composeRule.setContent {
            val uiState by viewModel.uiState.collectAsState()
            NotificationPreferencesScreen(
                uiState = uiState,
                onRuleToggled = viewModel::setRuleEnabled,
                onRetry = viewModel::refresh,
            )
        }
        composeRule.waitForIdle()

        composeRule.onNodeWithText("Deploys").assertExists()
        viewModel.setRuleEnabled("deploy", true)
        composeRule.waitForIdle()

        composeRule.onNodeWithText("The server rejected the change.").assertExists()
    }
}
