package dev.servercontrolpanel.feature.terminal.ui

import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import dev.servercontrolpanel.feature.terminal.transport.ConnectionState
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/**
 * Renders [ConnectionBanner] in every [ConnectionState] plus `Live` while stalled,
 * checking the real rendered text so a stalled connection never looks healthy.
 */
@RunWith(RobolectricTestRunner::class)
class ConnectionBannerTest {

    @get:Rule
    val composeRule = createComposeRule()

    @Test
    fun `a healthy live connection shows no banner text at all`() {
        composeRule.setContent {
            ConnectionBanner(state = ConnectionState.Live, isStalled = false)
        }

        composeRule.onNodeWithText("Connecting…").assertDoesNotExist()
        composeRule.onNodeWithText("Disconnected").assertDoesNotExist()
    }

    @Test
    fun `a stalled live connection is not silently indistinguishable from healthy`() {
        composeRule.setContent {
            ConnectionBanner(state = ConnectionState.Live, isStalled = true)
        }

        composeRule.onNodeWithText("No response from the server — the connection seems stuck").assertExists()
    }

    @Test
    fun `connecting shows its own label`() {
        composeRule.setContent {
            ConnectionBanner(state = ConnectionState.Connecting, isStalled = false)
        }

        composeRule.onNodeWithText("Connecting…").assertExists()
    }

    @Test
    fun `reconnecting includes the attempt number`() {
        composeRule.setContent {
            ConnectionBanner(state = ConnectionState.Reconnecting(attempt = 3), isStalled = false)
        }

        composeRule.onNodeWithText("Reconnecting (attempt 3)…").assertExists()
    }

    @Test
    fun `disconnected shows its own label`() {
        composeRule.setContent {
            ConnectionBanner(state = ConnectionState.Disconnected, isStalled = false)
        }

        composeRule.onNodeWithText("Disconnected").assertExists()
    }

    @Test
    fun `a session ended server-side is distinguished from a plain disconnect`() {
        composeRule.setContent {
            ConnectionBanner(state = ConnectionState.SessionEnded, isStalled = false)
        }

        composeRule.onNodeWithText("This session was ended on the server").assertExists()
    }

    @Test
    fun `a failure surfaces the underlying reason`() {
        composeRule.setContent {
            ConnectionBanner(state = ConnectionState.Failed(reason = "Ticket expired"), isStalled = false)
        }

        composeRule.onNodeWithText("Connection failed: Ticket expired").assertExists()
    }
}
