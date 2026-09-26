package com.vpsmanager.app

import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/**
 * Renders the stateless [DiagnosticsScreen] for every combination of `initFailures` and
 * `lastCrash`.
 */
@RunWith(RobolectricTestRunner::class)
class DiagnosticsScreenTest {

    @get:Rule
    val composeRule = createComposeRule()

    @Test
    fun `no failures and no crash still renders the screen and its clear action`() {
        var cleared = false
        composeRule.setContent {
            DiagnosticsScreen(initFailures = emptyList(), lastCrash = null, onClear = { cleared = true })
        }

        composeRule.onNodeWithText("Startup diagnostics").assertExists()
        composeRule.onNodeWithText("Clear and try opening the app").performClick()
        assert(cleared) { "expected onClear to fire" }
    }

    @Test
    fun `init failures are listed one per line`() {
        composeRule.setContent {
            DiagnosticsScreen(
                initFailures = listOf("ServerConfigStore: decryption failed", "PhoneAccountRegistrar: permission denied"),
                lastCrash = null,
                onClear = {},
            )
        }

        composeRule.onNodeWithText("Failed steps").assertExists()
        composeRule.onNodeWithText("• ServerConfigStore: decryption failed").assertExists()
        composeRule.onNodeWithText("• PhoneAccountRegistrar: permission denied").assertExists()
    }

    @Test
    fun `a previous crash renders its full stack trace selectably`() {
        val stackTrace = "java.lang.IllegalStateException: bootstrap failed\n\tat com.vpsmanager.app.Bootstrap.run(Bootstrap.kt:42)"
        composeRule.setContent {
            DiagnosticsScreen(initFailures = emptyList(), lastCrash = stackTrace, onClear = {})
        }

        composeRule.onNodeWithText("Previous process crash").assertExists()
        composeRule.onNodeWithText(stackTrace).assertExists()
    }

    @Test
    fun `both init failures and a previous crash render together`() {
        composeRule.setContent {
            DiagnosticsScreen(
                initFailures = listOf("Bootstrap: timeout"),
                lastCrash = "java.lang.RuntimeException: crash",
                onClear = {},
            )
        }

        composeRule.onNodeWithText("Failed steps").assertExists()
        composeRule.onNodeWithText("Previous process crash").assertExists()
    }
}
