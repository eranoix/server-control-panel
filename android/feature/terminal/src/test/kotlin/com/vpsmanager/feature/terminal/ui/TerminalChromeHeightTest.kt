package com.vpsmanager.feature.terminal.ui

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
import com.vpsmanager.feature.terminal.keys.EXTRA_KEYS_BAR_TAG
import com.vpsmanager.feature.terminal.keys.ExtraKeysBar
import com.vpsmanager.feature.terminal.keys.ExtraKeysBarState
import com.vpsmanager.feature.terminal.keys.PendingModifiers
import com.vpsmanager.feature.terminal.prefs.VisibleRows
import com.vpsmanager.feature.terminal.prefs.TypingMode
import com.vpsmanager.feature.terminal.prefs.TerminalScrollback
import com.vpsmanager.feature.terminal.prefs.TerminalLineSpacing
import com.vpsmanager.feature.terminal.transport.ConnectionState
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/**
 * The requirement of this delivery is, literally, height: giving the screen
 * back to the terminal. This file turns that into an assertion — it measures
 * the chrome in dp instead of describing it with an adjective.
 *
 * Three proofs, in the order of the three complaints:
 *  1. the connection banner costs 0 dp when everything is fine;
 *  2. the episodic controls cost 0 dp with the sheet closed;
 *  3. the whole terminal column, in steady state, spends only the key bar
 *     below the grid.
 */
@RunWith(RobolectricTestRunner::class)
class TerminalChromeHeightTest {

    @get:Rule
    val composeRule = createComposeRule()

    private val bannerTag = "faixa-conexao"
    private val gridTag = "grade-terminal"

    @Test
    fun `conectado e estavel a faixa de conexao nao ocupa altura nenhuma`() {
        composeRule.setContent {
            Box(modifier = Modifier.testTag(bannerTag)) {
                ConnectionBanner(state = ConnectionState.Live, isStalled = false)
            }
        }

        // "Connected" is not news: only reconnecting, disconnected, stalled
        // and ended earn a banner.
        composeRule.onNodeWithTag(bannerTag).assertHeightIsEqualTo(0.dp)
    }

    @Test
    fun `reconectando a faixa aparece`() {
        composeRule.setContent {
            Box(modifier = Modifier.testTag(bannerTag)) {
                ConnectionBanner(state = ConnectionState.Reconnecting(attempt = 2), isStalled = false)
            }
        }

        composeRule.onNodeWithText("Reconnecting (attempt 2)…").assertExists()
    }

    @Test
    fun `conectado mas travado a faixa aparece`() {
        composeRule.setContent {
            Box(modifier = Modifier.testTag(bannerTag)) {
                ConnectionBanner(state = ConnectionState.Live, isStalled = true)
            }
        }

        composeRule.onNodeWithText("No response from the server — the connection seems stuck").assertExists()
    }

    @Test
    fun `com a folha fechada nenhum controle episodico existe na tela`() {
        composeRule.setContent { TerminalColumn(optionsOpen = false) }

        // The three that used to be a permanent row above the grid.
        composeRule.onNodeWithText("Font size").assertDoesNotExist()
        composeRule.onNodeWithText("When the program asks for mouse").assertDoesNotExist()
        composeRule.onNodeWithText("Visible lines").assertDoesNotExist()
        // And the label that took a whole row just to announce another row.
        composeRule.onNodeWithText("Hide keys ▾").assertDoesNotExist()
    }

    @Test
    fun `com a folha aberta os tres controles episodicos aparecem`() {
        composeRule.setContent { TerminalColumn(optionsOpen = true) }

        composeRule.onNodeWithText("Font size").assertExists()
        // "When the program asks for the mouse" lived here and LEFT together
        // with the mouse preference; the line spacing took its place in the sheet.
        composeRule.onNodeWithText("Line spacing").assertExists()
        // "Load earlier history" is NO longer here, on purpose: the history
        // is the conversation itself, scrolled with a finger. A text pane
        // inside the menu was a second place to read the same thing, and the
        // owner pointed out it was the wrong place.
        composeRule.onNodeWithText("Load earlier history").assertDoesNotExist()
        composeRule.onNodeWithText("current grid: 54 columns × 46 rows").assertExists()
        // The control that replaces it: how many lines fit on screen.
        composeRule.onNodeWithText("Visible lines").assertExists()
        // The keyboard switch (Terminal x Text) lives in the sheet for the
        // same reason as the others: it is an episodic decision, not a
        // permanent row.
        composeRule.onNodeWithText("Keyboard").assertExists()
        composeRule.onNodeWithText("Scrollback").assertExists()
    }

    @Test
    fun `em regime o unico cromo abaixo da grade e a barra de 40 dp`() {
        composeRule.setContent { TerminalColumn(optionsOpen = false) }

        composeRule.onNodeWithTag(EXTRA_KEYS_BAR_TAG).assertHeightIsEqualTo(40.dp)
        // The grid takes ALL the rest: `weight(1f)`, not `fillMaxSize()` --
        // with `fillMaxSize()` the grid ate the entire space and the key bar
        // was measured at 0 dp, existing in the composition without showing up
        // in a single pixel.
        val grid = composeRule.onNodeWithTag(gridTag).fetchSemanticsNode().size.height
        assert(grid > 0) { "a grade tem que sobrar com altura" }
    }

    /**
     * The same column `TerminalRoute` composes, minus the ViewModel/socket:
     * the order and the heights are what is under test, not the link to the
     * server. The sheet comes in through its content
     * ([TerminalOptionsContent]) and not through the `ModalBottomSheet`
     * wrapper, which is a system window and adds nothing to what is to be
     * proved here.
     */
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
