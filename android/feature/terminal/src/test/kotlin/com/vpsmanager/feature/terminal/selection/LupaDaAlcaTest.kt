package com.vpsmanager.feature.terminal.selection

import androidx.compose.ui.geometry.Offset
import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * What this test protects: the magnifier has to stick to the LINE, not to the
 * finger.
 *
 * It is the only rule about the magnifier that is ours — the drawing, the zoom
 * and the animation are the manufacturer's. If the Y followed the finger, the
 * magnifier would tremble vertically with every wobble of the hand and would
 * show half of the line above with half of the one below, which is exactly
 * what it exists to avoid.
 */
class LupaDaAlcaTest {

    private val hitTester = CellHitTester(
        cellWidthPx = 10f,
        cellHeightPx = 20f,
        cols = 80,
        rows = 24,
    )

    @Test
    fun `o Y vai para o centro da celula, nao para o dedo`() {
        // Finger at y=25: inside row 1 (20..40), but near the top of it.
        val celula = hitTester.hitTest(Offset(x = 35f, y = 25f))
        val retangulo = hitTester.cellRect(celula.row, celula.col)

        assertEquals("linha sob o dedo", 1, celula.row)
        assertEquals("centro da linha, e nao o y do dedo", 30f, retangulo.center.y, 0.01f)
    }

    @Test
    fun `dedo oscilando dentro da mesma linha nao move a lupa`() {
        // The hand trembles a few pixels; the magnifier has to stay put.
        val alto = hitTester.hitTest(Offset(x = 35f, y = 21f))
        val baixo = hitTester.hitTest(Offset(x = 35f, y = 39f))

        val yAlto = hitTester.cellRect(alto.row, alto.col).center.y
        val yBaixo = hitTester.cellRect(baixo.row, baixo.col).center.y

        assertEquals("18 px de tremor na mesma linha = zero movimento da lupa", yAlto, yBaixo, 0.01f)
    }

    @Test
    fun `mudar de linha move a lupa uma linha inteira`() {
        val primeira = hitTester.hitTest(Offset(x = 35f, y = 25f))
        val segunda = hitTester.hitTest(Offset(x = 35f, y = 45f))

        val y1 = hitTester.cellRect(primeira.row, primeira.col).center.y
        val y2 = hitTester.cellRect(segunda.row, segunda.col).center.y

        assertEquals("uma altura de celula, exatamente", 20f, y2 - y1, 0.01f)
    }
}
