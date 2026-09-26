package com.vpsmanager.feature.notifications.prefs

import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.semantics.ProgressBarRangeInfo
import androidx.compose.ui.test.hasProgressBarRangeInfo
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import com.vpsmanager.data.push.NotifyPreferencesResult
import com.vpsmanager.data.push.NotifyPreferencesSource
import com.vpsmanager.data.push.NotifyRule
import com.vpsmanager.data.push.UpdateNotifyPreferencesResult
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/**
 * Renders [NotificationPreferencesScreen] under Robolectric across every
 * [NotificationPreferencesUiState] -- never composed before this. The
 * stateless screen is driven directly with hand-built states; the optimistic
 * toggle-then-revert-on-error flow is driven end to end through the real
 * [NotificationPreferencesViewModel] and a fake [NotifyPreferencesSource].
 */
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

    /**
     * Notifications is a top-level destination: the header is the shell's.
     * If anyone gives this screen a bar of its own, the app goes back to
     * stacking two headers — and with a "Back" that leaves a navigation
     * root, that is, that leaves nowhere. This test is the local lock; the
     * `AppNavHostTest` pins the same thing from the shell's side.
     */
    @Test
    fun `a tela nao traz cabecalho nem voltar proprios`() {
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
                uiState = NotificationPreferencesUiState.LoadError("Não foi possível carregar as preferências."),
                onRuleToggled = { _, _ -> },
                onRetry = {},
            )
        }

        composeRule.onNodeWithText("Não foi possível carregar as preferências.").assertExists()
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
                    errorMessage = "Não foi possível salvar a preferência.",
                ),
                onRuleToggled = { _, _ -> },
                onRetry = {},
            )
        }

        composeRule.onNodeWithText("Não foi possível salvar a preferência.").assertExists()
        composeRule.onNodeWithText("Deploys").assertExists()
    }

    @Test
    fun `toggling a rule that fails to save reverts it and surfaces the error`() {
        val source = object : NotifyPreferencesSource {
            override suspend fun fetch(deviceId: String) =
                NotifyPreferencesResult.Success(listOf(rule("deploy", "Deploys", enabled = false)))
            override suspend fun update(deviceId: String, enabledRuleIds: List<String>) =
                UpdateNotifyPreferencesResult.Error("O servidor recusou a alteração.")
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

        composeRule.onNodeWithText("O servidor recusou a alteração.").assertExists()
    }
}
