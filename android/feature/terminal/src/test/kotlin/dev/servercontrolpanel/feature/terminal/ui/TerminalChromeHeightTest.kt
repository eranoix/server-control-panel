package dev.servercontrolpanel.feature.terminal.ui

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.assertHeightIsEqualTo
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.unit.dp
import dev.servercontrolpanel.feature.terminal.keys.EXTRA_KEYS_BAR_TAG
import dev.servercontrolpanel.feature.terminal.keys.ExtraKeysBar
import dev.servercontrolpanel.feature.terminal.keys.ExtraKeysBarState
import dev.servercontrolpanel.feature.terminal.keys.PendingModifiers
import dev.servercontrolpanel.feature.terminal.prefs.VisibleRows
import dev.servercontrolpanel.feature.terminal.prefs.TypingMode
import dev.servercontrolpanel.feature.terminal.prefs.TerminalScrollback
import dev.servercontrolpanel.feature.terminal.prefs.TerminalLineSpacing
import dev.servercontrolpanel.feature.terminal.transport.ConnectionState
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

@RunWith(RobolectricTestRunner::class)
class TerminalChromeHeightTest {

    @get:Rule
    val composeRule = createComposeRule()

    private val bannerTag = "strip-connection"
    private val gridTag = "grade-terminal"

    @Test
    fun `connected and stable the connection banner takes no height`() {
        composeRule.setContent {
            Box(modifier = Modifier.testTag(bannerTag)) {
                ConnectionBanner(state = ConnectionState.Live, isStalled = false)
            }
        }

        composeRule.onNodeWithTag(bannerTag).assertHeightIsEqualTo(0.dp)
    }

    @Test
    fun `reconnecting shows the banner`() {
        composeRule.setContent {
            Box(modifier = Modifier.testTag(bannerTag)) {
                ConnectionBanner(state = ConnectionState.Reconnecting(attempt = 2), isStalled = false)
            }
        }

        composeRule.onNodeWithText("Reconnecting (attempt 2)…").assertExists()
    }

    @Test
    fun `connected but stalled shows the banner`() {
        composeRule.setContent {
            Box(modifier = Modifier.testTag(bannerTag)) {
                ConnectionBanner(state = ConnectionState.Live, isStalled = true)
            }
        }

        composeRule.onNodeWithText("No response from the server — the connection seems stuck").assertExists()
    }

    @Test
    fun `with the sheet closed no occasional control is on screen`() {
        composeRule.setContent { TerminalColumn(optionsOpen = false) }

        composeRule.onNodeWithText("Font size").assertDoesNotExist()
        composeRule.onNodeWithText("When the program asks for mouse").assertDoesNotExist()
        composeRule.onNodeWithText("Visible lines").assertDoesNotExist()
        composeRule.onNodeWithText("Hide keys ▾").assertDoesNotExist()
    }

    @Test
    fun `with the sheet open the occasional controls appear`() {
        composeRule.setContent { TerminalColumn(optionsOpen = true) }

        composeRule.onNodeWithText("Font size").assertExists()
        composeRule.onNodeWithText("Line spacing").assertExists()
        composeRule.onNodeWithText("Load earlier history").assertDoesNotExist()
        composeRule.onNodeWithText("current grid: 54 columns × 46 rows").assertExists()
        composeRule.onNodeWithText("Visible lines").assertExists()
        composeRule.onNodeWithText("Keyboard").assertExists()
        composeRule.onNodeWithText("Scrollback").assertExists()
    }

    @Test
    fun `in steady state the only chrome below the grid is the 40 dp bar`() {
        composeRule.setContent { TerminalColumn(optionsOpen = false) }

        composeRule.onNodeWithTag(EXTRA_KEYS_BAR_TAG).assertHeightIsEqualTo(40.dp)
        val grid = composeRule.onNodeWithTag(gridTag).fetchSemanticsNode().size.height
        assert(grid > 0) { "the grid must keep some height" }
    }

    @androidx.compose.runtime.Composable
    private fun TerminalColumn(optionsOpen: Boolean) {
        var keysBarState by remember { mutableStateOf(ExtraKeysBarState.ONE_ROW) }
        Column(modifier = Modifier.fillMaxSize()) {
            ConnectionBanner(state = ConnectionState.Live, isStalled = false)
            Box(
                modifier = Modifier
                    .weight(1f)
                    .fillMaxWidth()
                    .testTag(gridTag),
            )
            ExtraKeysBar(
                pendingModifiers = PendingModifiers(),
                onSendBytes = {},
                state = keysBarState,
                onStateChange = { keysBarState = it },
            )
        }
        if (optionsOpen) {
            TerminalOptionsContent(
                fontSizeSp = 16f,
                onFontSizeChange = {},
                gridCols = 54,
                gridRows = 46,
                scrollbackLines = TerminalScrollback.DEFAULT.lines,
                onScrollbackChange = {},
                typingMode = TypingMode.DEFAULT,
                onTypingModeChange = {},
                lineSpacing = TerminalLineSpacing.NORMAL,
                onLineSpacingChange = {},
                onPaste = {},
                onAttach = {},
                onShowKeyboard = {},
                batteryExempt = true,
                onRequestBatteryExemption = {},
                visibleRows = VisibleRows.AUTOMATIC,
                onVisibleRowsChange = {},
                visibleHeightPx = 1_800,
                referenceHeightPx = 1_800,
            )
        }
    }
}
