package dev.servercontrolpanel.feature.terminal.keys

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

@RunWith(RobolectricTestRunner::class)
class ExtraKeysBarScreenTest {

    @get:Rule
    val composeRule = createComposeRule()

    private fun setBar(
        initial: ExtraKeysBarState = ExtraKeysBarState.ONE_ROW,
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
    fun `the collapsed state is 32 dp and keeps the terminal usable`() {
        setBar(initial = ExtraKeysBarState.COLLAPSED)

        composeRule.onNodeWithTag(EXTRA_KEYS_BAR_TAG).assertHeightIsEqualTo(32.dp)
        composeRule.onNodeWithText("Esc").assertExists()
        composeRule.onNodeWithText("^C").assertExists()
        composeRule.onNodeWithText("Tab").assertExists()
        composeRule.onNodeWithText("Ctrl").assertExists()
        composeRule.onNodeWithText("PgUp").assertDoesNotExist()
    }

    @Test
    fun `the one-row state is 40 dp and has the eight keys`() {
        setBar(initial = ExtraKeysBarState.ONE_ROW)

        composeRule.onNodeWithTag(EXTRA_KEYS_BAR_TAG).assertHeightIsEqualTo(40.dp)
        listOf("Esc", "Tab", "Ctrl", "Alt", "←", "↓", "↑", "→").forEach { label ->
            composeRule.onNodeWithText(label).assertExists()
        }
    }

    @Test
    fun `the two-row state is 80 dp and has full navigation`() {
        setBar(initial = ExtraKeysBarState.TWO_ROWS)

        composeRule.onNodeWithTag(EXTRA_KEYS_BAR_TAG).assertHeightIsEqualTo(80.dp)
        listOf("Home", "End", "PgUp", "PgDn", "/").forEach { label ->
            composeRule.onNodeWithText(label).assertExists()
        }
    }

    @Test
    fun `tapping the handle cycles through the three states`() {
        setBar(initial = ExtraKeysBarState.COLLAPSED)

        composeRule.onNodeWithTag(EXTRA_KEYS_BAR_TAG).assertHeightIsEqualTo(32.dp)
        composeRule.onNodeWithTag(EXTRA_KEYS_HANDLE_TAG).performClick()
        composeRule.onNodeWithTag(EXTRA_KEYS_BAR_TAG).assertHeightIsEqualTo(40.dp)
        composeRule.onNodeWithTag(EXTRA_KEYS_HANDLE_TAG).performClick()
        composeRule.onNodeWithTag(EXTRA_KEYS_BAR_TAG).assertHeightIsEqualTo(80.dp)
        composeRule.onNodeWithTag(EXTRA_KEYS_HANDLE_TAG).performClick()
        composeRule.onNodeWithTag(EXTRA_KEYS_BAR_TAG).assertHeightIsEqualTo(32.dp)
    }

    @Test
    fun `dragging the handle up expands and down collapses`() {
        setBar(initial = ExtraKeysBarState.ONE_ROW)

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
    fun `a connected physical keyboard collapses the bar on its own`() {
        setBar(initial = ExtraKeysBarState.TWO_ROWS, hasHardwareKeyboard = true)

        composeRule.onNodeWithTag(EXTRA_KEYS_BAR_TAG).assertHeightIsEqualTo(32.dp)
    }

    @Test
    fun `auto-collapse can be undone with the handle`() {
        setBar(initial = ExtraKeysBarState.ONE_ROW, hasHardwareKeyboard = true)

        composeRule.onNodeWithTag(EXTRA_KEYS_BAR_TAG).assertHeightIsEqualTo(32.dp)
        composeRule.onNodeWithTag(EXTRA_KEYS_HANDLE_TAG).performClick()
        composeRule.onNodeWithTag(EXTRA_KEYS_BAR_TAG).assertHeightIsEqualTo(40.dp)
    }

    @Test
    fun `without a physical keyboard auto-collapse leaves the user's choice alone`() {
        setBar(initial = ExtraKeysBarState.COLLAPSED, hasHardwareKeyboard = false)

        composeRule.onNodeWithTag(EXTRA_KEYS_BAR_TAG).assertHeightIsEqualTo(32.dp)
        assertEquals(ExtraKeysBarState.ONE_ROW, ExtraKeysBarState.forHardwareKeyboard(present = false))
        assertEquals(ExtraKeysBarState.COLLAPSED, ExtraKeysBarState.forHardwareKeyboard(present = true))
    }

    @Test
    fun `tapping Esc sends ESC and tapping Ctrl arms the sticky modifier`() {
        val sent = mutableListOf<ByteArray>()
        val pending = PendingModifiers()
        setBar(onSendBytes = { sent.add(it) }, pendingModifiers = pending)

        composeRule.onNodeWithText("Esc").performClick()
        assertEquals(1, sent.size)
        assertArrayEquals(byteArrayOf(0x1b), sent[0])

        composeRule.onNodeWithText("Ctrl").performClick()
        assertTrue("one tap on Ctrl arms the sticky modifier", pending.isCtrlPending())
        assertEquals(ModifierArmState.ARMED, pending.ctrl)
    }

    @Test
    fun `swipe-up on a key fires the secondary function instead of the primary`() {
        val sent = mutableListOf<ByteArray>()
        setBar(onSendBytes = { sent.add(it) })

        composeRule.onNodeWithText("Esc").performTouchInput {
            swipeUp(startY = bottom, endY = top - 300f)
        }

        assertEquals(1, sent.size)
        assertArrayEquals(byteArrayOf(0x03), sent[0])
    }

    @Test
    fun `swipe-up on the left arrow sends Home`() {
        val sent = mutableListOf<ByteArray>()
        setBar(onSendBytes = { sent.add(it) })

        composeRule.onNodeWithText("←").performTouchInput {
            swipeUp(startY = bottom, endY = top - 300f)
        }

        assertEquals(1, sent.size)
        assertArrayEquals("[H".toByteArray(Charsets.US_ASCII), sent[0])
    }

    @Test
    fun `a single tap on an arrow does not repeat`() {
        val sent = mutableListOf<ByteArray>()
        setBar(onSendBytes = { sent.add(it) })

        composeRule.onNodeWithText("→").performTouchInput { click(Offset(centerX, centerY)) }

        assertEquals("one tap means one arrow", 1, sent.size)
        assertArrayEquals("[C".toByteArray(Charsets.US_ASCII), sent[0])
    }
}
