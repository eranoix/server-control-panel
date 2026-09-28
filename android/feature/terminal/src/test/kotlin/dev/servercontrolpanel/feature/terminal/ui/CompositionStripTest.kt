package dev.servercontrolpanel.feature.terminal.ui

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.test.assertHeightIsEqualTo
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.unit.dp
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

@RunWith(RobolectricTestRunner::class)
class CompositionStripTest {

    @get:Rule
    val composeRule = createComposeRule()

    @Test
    fun `in terminal mode with no composition the strip emits no node`() {
        composeRule.setContent {
            Column(modifier = Modifier.fillMaxSize()) {
                CompositionStrip(text = "", reserveSpace = false)
            }
        }

        composeRule.onNodeWithTag(COMPOSITION_STRIP_TAG).assertDoesNotExist()
    }

    @Test
    fun `in text mode the height is the same whether composing or not`() {
        var text by mutableStateOf("")
        composeRule.setContent {
            Column(modifier = Modifier.fillMaxSize()) {
                CompositionStrip(text = text, reserveSpace = true)
            }
        }

        val emptyHeight = composeRule.onNodeWithTag(COMPOSITION_STRIP_TAG)
            .fetchSemanticsNode().size.height
        assertTrue("the reserved strip must take up real height", emptyHeight > 0)

        text = "starting"
        composeRule.waitForIdle()
        val composingHeight = composeRule.onNodeWithTag(COMPOSITION_STRIP_TAG)
            .fetchSemanticsNode().size.height

        assertEquals(
            "the height changed between composing and not composing, which resizes the grid",
            emptyHeight,
            composingHeight,
        )

        text = ""
        composeRule.waitForIdle()
        assertEquals(
            "releasing the word changed the height back, so the oscillation is back",
            emptyHeight,
            composeRule.onNodeWithTag(COMPOSITION_STRIP_TAG).fetchSemanticsNode().size.height,
        )
    }

    @Test
    fun `with a word in flight the strip shows exactly what the keyboard is holding`() {
        composeRule.setContent {
            Column(modifier = Modifier.fillMaxSize()) { CompositionStrip(text = "star") }
        }

        composeRule.onNodeWithTag(COMPOSITION_STRIP_TAG).assertExists()
        composeRule.onNodeWithText("star").assertExists()
    }

    @Test
    fun `the strip takes one line regardless of word length`() {
        var text by mutableStateOf("hi")
        composeRule.setContent {
            Column(modifier = Modifier.fillMaxSize()) { CompositionStrip(text = text) }
        }

        val shortHeight = with(composeRule.density) {
            composeRule.onNodeWithTag(COMPOSITION_STRIP_TAG)
                .fetchSemanticsNode().size.height.toDp()
        }

        text = "supercalifragilisticexpialidocious-and-then-some-more-to-overflow-the-width"
        composeRule.waitForIdle()

        composeRule.onNodeWithTag(COMPOSITION_STRIP_TAG).assertHeightIsEqualTo(shortHeight)
    }
}
