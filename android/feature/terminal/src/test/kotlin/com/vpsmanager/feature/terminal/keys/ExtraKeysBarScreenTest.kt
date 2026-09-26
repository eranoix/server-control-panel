package com.vpsmanager.feature.terminal.keys

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.test.assertHeightIsEqualTo
import androidx.compose.ui.test.click
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTouchInput
import androidx.compose.ui.test.swipeDown
import androidx.compose.ui.test.swipeUp
import androidx.compose.ui.unit.dp
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/**
 * The bar's three states, the auto-collapse with a physical keyboard and the
 * secondary function on swipe-up — rendered for real under Robolectric.
 *
 * Height is a first-class assertion here, not an aesthetic detail: the bar is
 * the only permanent chrome left below the grid, so "how much room it takes"
 * IS the requirement. [ExtraKeysBarActionTest] covers the byte encoding with
 * no composition; this file covers what only exists once composed.
 */
@RunWith(RobolectricTestRunner::class)
class ExtraKeysBarScreenTest {

    @get:Rule
    val composeRule = createComposeRule()

    /** Host holding the bar's state, just like `TerminalRoute` does. */
    private fun setBar(
        initial: ExtraKeysBarState = ExtraKeysBarState.UMA_LINHA,
        hasHardwareKeyboard: Boolean = false,
        onSendBytes: (ByteArray) -> Unit = {},
        pendingModifiers: PendingModifiers = PendingModifiers(),
    ) {
        composeRule.setContent {
            var state by remember { mutableStateOf(initial) }
            Column(modifier = Modifier.fillMaxSize()) {
                ExtraKeysBar(
                    pendingModifiers = pendingModifiers,
                    onSendBytes = onSendBytes,
                    state = state,
                    onStateChange = { state = it },
                    hasHardwareKeyboard = hasHardwareKeyboard,
                )
            }
        }
    }

    @Test
    fun `estado colapsado mede 32 dp e mantem o terminal operavel`() {
        setBar(initial = ExtraKeysBarState.COLAPSADA)

        composeRule.onNodeWithTag(EXTRA_KEYS_BAR_TAG).assertHeightIsEqualTo(32.dp)
        // The minimum set: without these, neither Ctrl+C nor quitting vim.
        composeRule.onNodeWithText("Esc").assertExists()
        composeRule.onNodeWithText("^C").assertExists()
        composeRule.onNodeWithText("Tab").assertExists()
        composeRule.onNodeWithText("Ctrl").assertExists()
        // Keys that only exist in the larger states stay out.
        composeRule.onNodeWithText("PgUp").assertDoesNotExist()
    }

    @Test
    fun `estado de uma linha mede 40 dp e traz as oito teclas`() {
        setBar(initial = ExtraKeysBarState.UMA_LINHA)

        composeRule.onNodeWithTag(EXTRA_KEYS_BAR_TAG).assertHeightIsEqualTo(40.dp)
        listOf("Esc", "Tab", "Ctrl", "Alt", "←", "↓", "↑", "→").forEach { label ->
            composeRule.onNodeWithText(label).assertExists()
        }
    }

    @Test
    fun `estado de duas linhas mede 80 dp e traz a navegacao completa`() {
        setBar(initial = ExtraKeysBarState.DUAS_LINHAS)

        composeRule.onNodeWithTag(EXTRA_KEYS_BAR_TAG).assertHeightIsEqualTo(80.dp)
        listOf("Home", "End", "PgUp", "PgDn", "/").forEach { label ->
            composeRule.onNodeWithText(label).assertExists()
        }
    }

    @Test
    fun `a alca cicla os tres estados no toque`() {
        setBar(initial = ExtraKeysBarState.COLAPSADA)

        composeRule.onNodeWithTag(EXTRA_KEYS_BAR_TAG).assertHeightIsEqualTo(32.dp)
        composeRule.onNodeWithTag(EXTRA_KEYS_HANDLE_TAG).performClick()
        composeRule.onNodeWithTag(EXTRA_KEYS_BAR_TAG).assertHeightIsEqualTo(40.dp)
        composeRule.onNodeWithTag(EXTRA_KEYS_HANDLE_TAG).performClick()
        composeRule.onNodeWithTag(EXTRA_KEYS_BAR_TAG).assertHeightIsEqualTo(80.dp)
        composeRule.onNodeWithTag(EXTRA_KEYS_HANDLE_TAG).performClick()
        composeRule.onNodeWithTag(EXTRA_KEYS_BAR_TAG).assertHeightIsEqualTo(32.dp)
    }

    @Test
    fun `arrastar a alca pra cima expande e pra baixo colapsa`() {
        setBar(initial = ExtraKeysBarState.UMA_LINHA)

        composeRule.onNodeWithTag(EXTRA_KEYS_HANDLE_TAG).performTouchInput {
            swipeUp(startY = bottom, endY = top - 200f)
        }
        composeRule.onNodeWithTag(EXTRA_KEYS_BAR_TAG).assertHeightIsEqualTo(80.dp)

        composeRule.onNodeWithTag(EXTRA_KEYS_HANDLE_TAG).performTouchInput {
            swipeDown(startY = top, endY = bottom + 200f)
        }
        composeRule.onNodeWithTag(EXTRA_KEYS_BAR_TAG).assertHeightIsEqualTo(40.dp)
    }

    @Test
    fun `teclado fisico conectado colapsa a barra sozinho`() {
        setBar(initial = ExtraKeysBarState.DUAS_LINHAS, hasHardwareKeyboard = true)

        // With no tap at all: the mere presence of a physical keyboard already
        // takes the bar to the collapsed state (the opposite of the complaint
        // open for years in Termux and of the bug acknowledged in JuiceSSH's
        // FAQ).
        composeRule.onNodeWithTag(EXTRA_KEYS_BAR_TAG).assertHeightIsEqualTo(32.dp)
    }

    @Test
    fun `o auto-colapso e reversivel pela alca`() {
        setBar(initial = ExtraKeysBarState.UMA_LINHA, hasHardwareKeyboard = true)

        composeRule.onNodeWithTag(EXTRA_KEYS_BAR_TAG).assertHeightIsEqualTo(32.dp)
        composeRule.onNodeWithTag(EXTRA_KEYS_HANDLE_TAG).performClick()
        // With a physical keyboard still connected, the handle is still in
        // charge: the automatism does not lock the operator out of the arrows.
        composeRule.onNodeWithTag(EXTRA_KEYS_BAR_TAG).assertHeightIsEqualTo(40.dp)
    }

    @Test
    fun `sem teclado fisico o automatismo nao mexe no que o operador escolheu`() {
        setBar(initial = ExtraKeysBarState.COLAPSADA, hasHardwareKeyboard = false)

        // Entering the screen with no physical keyboard does not reimpose
        // UMA_LINHA over the handle's choice: the automatism only acts when
        // there IS a physical keyboard, or when one already evaluated
        // disconnects.
        composeRule.onNodeWithTag(EXTRA_KEYS_BAR_TAG).assertHeightIsEqualTo(32.dp)
        assertEquals(ExtraKeysBarState.UMA_LINHA, ExtraKeysBarState.forHardwareKeyboard(present = false))
        assertEquals(ExtraKeysBarState.COLAPSADA, ExtraKeysBarState.forHardwareKeyboard(present = true))
    }

    @Test
    fun `tocar Esc manda ESC e tocar Ctrl arma o modificador sticky`() {
        val sent = mutableListOf<ByteArray>()
        val pending = PendingModifiers()
        setBar(onSendBytes = { sent.add(it) }, pendingModifiers = pending)

        composeRule.onNodeWithText("Esc").performClick()
        assertEquals(1, sent.size)
        assertArrayEquals(byteArrayOf(0x1b), sent[0])

        composeRule.onNodeWithText("Ctrl").performClick()
        assertTrue("um toque em Ctrl arma o sticky", pending.isCtrlPending())
        assertEquals(ModifierArmState.ARMED, pending.ctrl)
    }

    @Test
    fun `swipe-up numa tecla dispara a segunda funcao no lugar da primeira`() {
        val sent = mutableListOf<ByteArray>()
        setBar(onSendBytes = { sent.add(it) })

        composeRule.onNodeWithText("Esc").performTouchInput {
            swipeUp(startY = bottom, endY = top - 300f)
        }

        // Esc's secondary function is Ctrl+C (0x03) -- and the ESC (0x1b) of
        // the primary function does NOT go out with it: the gesture is a single
        // finger, and once recognised as a swipe it cancels the tap.
        assertEquals(1, sent.size)
        assertArrayEquals(byteArrayOf(0x03), sent[0])
    }

    @Test
    fun `swipe-up na seta esquerda manda Home`() {
        val sent = mutableListOf<ByteArray>()
        setBar(onSendBytes = { sent.add(it) })

        composeRule.onNodeWithText("←").performTouchInput {
            swipeUp(startY = bottom, endY = top - 300f)
        }

        assertEquals(1, sent.size)
        assertArrayEquals("[H".toByteArray(Charsets.US_ASCII), sent[0])
    }

    @Test
    fun `um toque simples na seta nao repete`() {
        val sent = mutableListOf<ByteArray>()
        setBar(onSendBytes = { sent.add(it) })

        composeRule.onNodeWithText("→").performTouchInput { click(Offset(centerX, centerY)) }

        assertEquals("um toque = uma seta", 1, sent.size)
        assertArrayEquals("[C".toByteArray(Charsets.US_ASCII), sent[0])
    }
}
