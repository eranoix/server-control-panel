package com.vpsmanager.feature.terminal.ui

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

/**
 * The composition band is what makes text mode usable — and it is also more
 * chrome between the grid and the keyboard, which is exactly what the operator
 * has already complained about. These tests pin down both halves: it appears
 * when there is a word in flight, and it **does not exist** when there is not.
 */
@RunWith(RobolectricTestRunner::class)
class FaixaDeComposicaoTest {

    @get:Rule
    val composeRule = createComposeRule()

    @Test
    fun `no modo terminal, sem composicao, a faixa nao emite no nenhum`() {
        // Here disappearing entirely is still safe: with no composition, the
        // band will never come back until the mode changes — so no flicker is
        // possible.
        composeRule.setContent {
            Column(modifier = Modifier.fillMaxSize()) {
                FaixaDeComposicao(texto = "", reservarEspaco = false)
            }
        }

        composeRule.onNodeWithTag(FAIXA_COMPOSICAO_TAG).assertDoesNotExist()
    }

    /**
     * THE HEIGHT MUST NOT CHANGE BETWEEN COMPOSING AND NOT COMPOSING.
     *
     * It was that height changing that resized the grid on every word the
     * corrector held and released: 77 resizes in 45 minutes, oscillating
     * between 48 and 50 rows, measured in the server's log with the owner
     * using the app. Each resize is a SIGWINCH, each SIGWINCH is a whole
     * repaint, and a frame taller than the screen cannot erase itself — the
     * screen ended up with its rows overlaid and the characters interleaved.
     *
     * This test compares the two heights directly. If anyone gives the band
     * back its "0 dp cost", they fall over here and not on the owner's device.
     */
    @Test
    fun `no modo texto a altura e a MESMA compondo ou nao`() {
        // ONE composition, with the text changing — which is what really
        // happens: the band alternates inside the same live screen, and it was
        // that alternation that resized the grid.
        var texto by mutableStateOf("")
        composeRule.setContent {
            Column(modifier = Modifier.fillMaxSize()) {
                FaixaDeComposicao(texto = texto, reservarEspaco = true)
            }
        }

        val alturaVazia = composeRule.onNodeWithTag(FAIXA_COMPOSICAO_TAG)
            .fetchSemanticsNode().size.height
        assertTrue("a faixa reservada tem que ocupar altura de verdade", alturaVazia > 0)

        texto = "comecando"
        composeRule.waitForIdle()
        val alturaCompondo = composeRule.onNodeWithTag(FAIXA_COMPOSICAO_TAG)
            .fetchSemanticsNode().size.height

        assertEquals(
            "a altura mudou entre compor e nao compor — e isso redimensiona a grade",
            alturaVazia,
            alturaCompondo,
        )

        // And back again: releasing the word must not move the height either.
        texto = ""
        composeRule.waitForIdle()
        assertEquals(
            "soltar a palavra devolveu a altura antiga — a oscilacao voltou",
            alturaVazia,
            composeRule.onNodeWithTag(FAIXA_COMPOSICAO_TAG).fetchSemanticsNode().size.height,
        )
    }

    @Test
    fun `com palavra em voo a faixa mostra exatamente o que o teclado esta segurando`() {
        composeRule.setContent {
            Column(modifier = Modifier.fillMaxSize()) { FaixaDeComposicao(texto = "comec") }
        }

        composeRule.onNodeWithTag(FAIXA_COMPOSICAO_TAG).assertExists()
        composeRule.onNodeWithText("comec").assertExists()
    }

    @Test
    fun `a faixa ocupa uma linha so, independente do tamanho da palavra`() {
        // A long word must not wrap onto two lines: wrapping would change the
        // height mid-typing and push the grid up on every word. Hence
        // `maxLines = 1` and horizontal scrolling.
        // The text changes WITHIN the same composition, as it really does
        // while typing — and not through two `setContent` calls, which the
        // test rule does not even allow.
        var texto by mutableStateOf("oi")
        composeRule.setContent {
            Column(modifier = Modifier.fillMaxSize()) { FaixaDeComposicao(texto = texto) }
        }

        val alturaCurta = with(composeRule.density) {
            composeRule.onNodeWithTag(FAIXA_COMPOSICAO_TAG)
                .fetchSemanticsNode().size.height.toDp()
        }

        texto = "supercalifragilisticexpialidocious-e-mais-um-tanto-para-estourar-a-largura"
        composeRule.waitForIdle()

        composeRule.onNodeWithTag(FAIXA_COMPOSICAO_TAG).assertHeightIsEqualTo(alturaCurta)
    }
}
