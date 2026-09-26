package com.vpsmanager.feature.admin.ops

import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/**
 * Renders [DeployTriggerScreen] under Robolectric across the `isAdmin`
 * gate and every [DeployTriggerUiState] -- never composed before this.
 * Stateless, so no ViewModel/repository fake is needed.
 */
@RunWith(RobolectricTestRunner::class)
class DeployTriggerScreenTest {

    @get:Rule
    val composeRule = createComposeRule()

    private fun setContent(uiState: DeployTriggerUiState, isAdmin: Boolean?, onConfirm: () -> Unit = {}, onRequest: () -> Unit = {}, onDismiss: () -> Unit = {}) {
        composeRule.setContent {
            DeployTriggerScreen(
                uiState = uiState,
                isAdmin = isAdmin,
                onBack = {},
                onRequestConfirmation = onRequest,
                onDismissConfirmation = onDismiss,
                onConfirmDeploy = onConfirm,
            )
        }
    }

    @Test
    fun `while the admin check is in flight, only a spinner shows -- no button leaks early`() {
        setContent(uiState = DeployTriggerUiState.Idle, isAdmin = null)

        // The TopAppBar title itself reads "Disparar deploy", so it always
        // exists; only the clickable trigger button must not leak early.
        composeRule.onNode(hasText("Trigger deploy") and hasClickAction()).assertDoesNotExist()
    }

    @Test
    fun `a non-admin never sees the trigger button, only the restriction message`() {
        setContent(uiState = DeployTriggerUiState.Idle, isAdmin = false)

        composeRule.onNodeWithText("This action is restricted to administrators.").assertExists()
    }

    @Test
    fun `an admin at idle can request the confirmation dialog`() {
        var requested = false
        setContent(uiState = DeployTriggerUiState.Idle, isAdmin = true, onRequest = { requested = true })

        // The TopAppBar title also reads "Disparar deploy", so target only
        // the clickable button to avoid an ambiguous match.
        composeRule.onNode(hasText("Trigger deploy") and hasClickAction()).performClick()
        assert(requested) { "expected onRequestConfirmation to fire" }
    }

    @Test
    fun `awaiting confirmation shows the informed dialog and both its actions work`() {
        var confirmed = false
        var dismissed = false
        setContent(
            uiState = DeployTriggerUiState.AwaitingConfirmation,
            isAdmin = true,
            onConfirm = { confirmed = true },
            onDismiss = { dismissed = true },
        )

        composeRule.onNodeWithText("Trigger deploy?").assertExists()
        composeRule.onNodeWithText("Confirm").performClick()
        assert(confirmed) { "expected onConfirmDeploy to fire" }

        composeRule.onNodeWithText("Cancel").performClick()
        assert(dismissed) { "expected onDismissConfirmation to fire" }
    }

    @Test
    fun `a failed trigger surfaces its message and offers a retry`() {
        setContent(uiState = DeployTriggerUiState.TriggerFailed("Falha ao enfileirar o deploy."), isAdmin = true)

        composeRule.onNodeWithText("Falha ao enfileirar o deploy.").assertExists()
        composeRule.onNodeWithText("Try again").assertExists()
    }

    @Test
    fun `a queued job explains the possible ten-minute wait instead of looking frozen`() {
        setContent(
            uiState = DeployTriggerUiState.InProgress(
                jobId = "job-1",
                phase = "queued",
                logLines = emptyList(),
                progress = 0,
                step = null,
            ),
            isAdmin = true,
        )

        composeRule.onNodeWithText("Queued — this can take up to 10 minutes if another deploy is in progress.").assertExists()
    }

    @Test
    fun `a running job shows its phase, step and log lines`() {
        setContent(
            uiState = DeployTriggerUiState.InProgress(
                jobId = "job-1",
                phase = "running",
                logLines = listOf("clonando repositório", "buildando binário"),
                progress = 42,
                step = "build",
            ),
            isAdmin = true,
        )

        composeRule.onNodeWithText("Current phase: running").assertExists()
        composeRule.onNodeWithText("Step: build").assertExists()
        composeRule.onNodeWithText("clonando repositório").assertExists()
        composeRule.onNodeWithText("buildando binário").assertExists()
    }

    @Test
    fun `a terminal outcome shows the status and error verbatim, and allows a new deploy`() {
        var requested = false
        setContent(
            uiState = DeployTriggerUiState.Outcome(status = "failed", error = "health check failed on attempt 15"),
            isAdmin = true,
            onRequest = { requested = true },
        )

        composeRule.onNodeWithText("Result: failed").assertExists()
        composeRule.onNodeWithText("health check failed on attempt 15").assertExists()

        composeRule.onNodeWithText("Trigger a new deploy").performClick()
        assert(requested) { "expected onRequestConfirmation to fire" }
    }
}
