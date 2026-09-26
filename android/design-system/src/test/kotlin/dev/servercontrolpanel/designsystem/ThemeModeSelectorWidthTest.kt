package dev.servercontrolpanel.designsystem

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.SemanticsNodeInteraction
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.text.TextLayoutResult
import androidx.compose.ui.unit.dp
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * The three labels fit in the drawer width (360dp sheet minus side padding).
 *
 * Robolectric has no real font, so `didOverflowWidth` is rounding noise. This
 * test instead checks one line per label and that each button offers far more
 * room than the text needs; real-font clipping is verified on an emulator.
 */
@RunWith(RobolectricTestRunner::class)
@Config(qualifiers = "w411dp-h891dp-xxhdpi")
class ThemeModeSelectorWidthTest {

    @get:Rule
    val composeRule = createComposeRule()

    /** Material 3 drawer sheet width. */
    private val drawerWidth = 360.dp

    /** Same padding `AppDrawerSheet` applies to the selector. */
    private val sidePadding = 28.dp

    private fun SemanticsNodeInteraction.textLayout(): TextLayoutResult {
        val results = mutableListOf<TextLayoutResult>()
        val action = fetchSemanticsNode().config[SemanticsActions.GetTextLayoutResult]
        requireNotNull(action.action) { "node has no GetTextLayoutResult; is it a text node?" }.invoke(results)
        return results.first()
    }

    private fun renderInDrawer() {
        composeRule.setContent {
            PanelTheme {
                Box(modifier = Modifier.width(drawerWidth)) {
                    ThemeModeSelector(
                        selected = ThemeMode.SYSTEM,
                        onSelect = {},
                        modifier = Modifier.padding(horizontal = sidePadding),
                    )
                }
            }
        }
        composeRule.waitForIdle()
    }

    @Test
    fun `each label fits on one line with room to spare`() {
        renderInDrawer()

        ThemeMode.entries.forEach { mode ->
            val layout = composeRule.onNodeWithText(mode.label).textLayout()
            assertEquals("label \"${mode.label}\" wrapped onto more than one line", 1, layout.lineCount)

            // Compare offered vs requested width; absolute widths are distorted by Robolectric's fake font.
            val offered = layout.layoutInput.constraints.maxWidth
            val request = layout.multiParagraph.maxIntrinsicWidth
            assertTrue(
                "the \"${mode.label}\" button offered $offered px for $request px of text",
                offered >= request * 2,
            )
        }
    }

    @Test
    fun `the three buttons split the width evenly`() {
        renderInDrawer()

        // A squeezed segment would clip the longest label first.
        val widths = ThemeMode.entries.map {
            composeRule.onNodeWithTag(themeOptionTag(it)).fetchSemanticsNode().size.width
        }
        assertTrue("segments have different widths: $widths", widths.max() - widths.min() <= 2)
        assertTrue("segments too narrow: $widths", widths.min() > 0)
    }
}
