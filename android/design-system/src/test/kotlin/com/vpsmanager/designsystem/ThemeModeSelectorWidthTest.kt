package com.vpsmanager.designsystem

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
 * The three labels fit in the width the selector actually lives in: a 360 dp
 * `ModalDrawerSheet` minus the drawer's lateral breathing room.
 *
 * ## What this test proves, and what it does NOT prove
 * Robolectric has no real font: each glyph measures about 1 px, and so
 * `didOverflowWidth` here is rounding noise (width 5.0 against an intrinsic
 * 5.5) and not a signal of a clipped label — using it would give a test that
 * fails on its own and catches no defect at all. What can be asserted on the
 * JVM is what is asserted below: the label fits on ONE line, and the button
 * offers it MUCH more room than it asks for (it is not squeezed by a layout
 * error — that is how this app's previous `NavigationBar` clipped
 * "Notific/ações"). **The proof with the real font is the emulator
 * screenshot**, taken in all three modes.
 *
 * It lives in `:design-system`, not in `:app`, because the component belongs
 * here: a new label, or a smaller gap, breaks the build in the module that
 * caused it.
 */
@RunWith(RobolectricTestRunner::class)
@Config(qualifiers = "w411dp-h891dp-xxhdpi")
class ThemeModeSelectorWidthTest {

    @get:Rule
    val composeRule = createComposeRule()

    /** The width of a Material 3 drawer sheet. */
    private val larguraDaGaveta = 360.dp

    /** The same breathing room `AppDrawerSheet` applies to the selector. */
    private val respiroLateral = 28.dp

    private fun SemanticsNodeInteraction.textLayout(): TextLayoutResult {
        val results = mutableListOf<TextLayoutResult>()
        val action = fetchSemanticsNode().config[SemanticsActions.GetTextLayoutResult]
        requireNotNull(action.action) { "nó sem GetTextLayoutResult — não é um texto?" }.invoke(results)
        return results.first()
    }

    private fun renderNaGaveta() {
        composeRule.setContent {
            VpsManagerTheme {
                Box(modifier = Modifier.width(larguraDaGaveta)) {
                    ThemeModeSelector(
                        selected = ThemeMode.SISTEMA,
                        onSelect = {},
                        modifier = Modifier.padding(horizontal = respiroLateral),
                    )
                }
            }
        }
        composeRule.waitForIdle()
    }

    @Test
    fun `cada rotulo cabe numa linha e sobra espaco no botao`() {
        renderNaGaveta()

        ThemeMode.entries.forEach { modo ->
            val layout = composeRule.onNodeWithText(modo.label).textLayout()
            assertEquals("rótulo \"${modo.label}\" quebrou em mais de uma linha", 1, layout.lineCount)

            // The space the button actually offered the text, against the
            // space the text asked for. It is this ratio — and not the
            // absolute width, which Robolectric's fake font distorts — that
            // exposes a button too tight for its label.
            val ofertado = layout.layoutInput.constraints.maxWidth
            val pedido = layout.multiParagraph.maxIntrinsicWidth
            assertTrue(
                "o botão de \"${modo.label}\" ofereceu $ofertado px para um texto de $pedido px",
                ofertado >= pedido * 2,
            )
        }
    }

    @Test
    fun `os tres botoes dividem a largura por igual`() {
        renderNaGaveta()

        // A segment squeezed relative to the others is the defect that would
        // make the longest label ("Sistema") the only one clipped on the device.
        val larguras = ThemeMode.entries.map {
            composeRule.onNodeWithTag(themeOptionTag(it)).fetchSemanticsNode().size.width
        }
        assertTrue("segmentos com larguras diferentes: $larguras", larguras.max() - larguras.min() <= 2)
        assertTrue("segmentos estreitos demais: $larguras", larguras.min() > 0)
    }
}
