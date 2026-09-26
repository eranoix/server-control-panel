package com.vpsmanager.feature.terminal.selection

import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/**
 * The draggable handles — what the terminal's selection was missing to feel
 * like that of an Android text field.
 *
 * Before there was only one gesture: long press, drag, release. Missed the end
 * by one cell? Start over from scratch. With handles, adjusting means grabbing
 * the wrong end and moving it — and the text that gets copied is the one
 * highlighted, not the one you remember having dragged.
 *
 * All the geometry comes from the SAME [CellHitTester] that maps a touch onto
 * a cell: one rounding convention only, there and back.
 */
class SelectionHandlesTest {

    private val hitTester = CellHitTester(cellWidthPx = 20f, cellHeightPx = 40f, cols = 10, rows = 5)

    @Test
    fun ancoras_pendemDasBordasInferioresDaPrimeiraEdaUltimaCelula() {
        // The Android convention: the left handle hangs from the bottom LEFT
        // edge of the first character, the right one from the bottom RIGHT of
        // the last — that is what makes each "point at" what it delimits.
        val selecao = GridSelection(startRow = 1, startCol = 2, endRow = 1, endCol = 4)

        val ancoras = handleAnchors(selecao, hitTester)

        assertEquals(Offset(40f, 80f), ancoras.inicio)
        assertEquals(Offset(100f, 80f), ancoras.fim)
    }

    @Test
    fun ancoras_naoTrocamDeLadoQuandoOarrasteFoiDeTrasPraFrente() {
        // Dragging right to left produces an "inverted" selection. If the
        // start handle jumped to the other side mid-gesture, it would run away
        // from the finger.
        val paraFrente = GridSelection(startRow = 0, startCol = 1, endRow = 0, endCol = 5)
        val paraTras = GridSelection(startRow = 0, startCol = 5, endRow = 0, endCol = 1)

        assertEquals(handleAnchors(paraFrente, hitTester), handleAnchors(paraTras, hitTester))
    }

    @Test
    fun oToqueSobreAalcaAcertaAalca() {
        val selecao = GridSelection(0, 2, 2, 6)
        val ancoras = handleAnchors(selecao, hitTester)

        assertEquals(SelectionHandle.INICIO, handleAt(ancoras.inicio, ancoras, raioPx = 48f))
        assertEquals(SelectionHandle.FIM, handleAt(ancoras.fim, ancoras, raioPx = 48f))
    }

    @Test
    fun oToqueLongeDasDuas_naoEdeAlcaNenhuma_eOgestoVoltaPraGrade() {
        val selecao = GridSelection(0, 2, 0, 4)
        val ancoras = handleAnchors(selecao, hitTester)

        assertNull(handleAt(Offset(180f, 180f), ancoras, raioPx = 48f))
    }

    @Test
    fun comAsDuasAoAlcance_venceAmaisProxima() {
        // A single-cell selection: the two anchors sit 20 px apart, inside
        // the same 48 dp target.
        val selecao = GridSelection(0, 0, 0, 0)
        val ancoras = handleAnchors(selecao, hitTester)

        assertEquals(SelectionHandle.INICIO, handleAt(Offset(2f, 40f), ancoras, raioPx = 48f))
        assertEquals(SelectionHandle.FIM, handleAt(Offset(19f, 40f), ancoras, raioPx = 48f))
    }

    @Test
    fun arrastarAalcaDeFim_moveSoAponta_deixandoOinicioNoLugar() {
        val holder = GridSelectionHolder()
        val controller = SelectionGestureController({ hitTester }, holder)
        controller.definirSelecao(GridSelection(0, 1, 0, 3))

        // Drop the end on the cell (row 2, column 7).
        controller.arrastarAlca(SelectionHandle.FIM, Offset(150f, 100f))

        assertEquals(GridSelection(0, 1, 2, 7), holder.selection)
    }

    @Test
    fun arrastarAalcaDeInicio_moveSoOcomeco() {
        val holder = GridSelectionHolder()
        val controller = SelectionGestureController({ hitTester }, holder)
        controller.definirSelecao(GridSelection(1, 4, 3, 8))

        controller.arrastarAlca(SelectionHandle.INICIO, Offset(10f, 10f))

        assertEquals(GridSelection(0, 0, 3, 8), holder.selection)
    }

    @Test
    fun asAlcasPodemSeCruzar_eOtextoSaiEmOrdemDeLeitura() {
        val holder = GridSelectionHolder()
        val controller = SelectionGestureController({ hitTester }, holder)
        controller.definirSelecao(GridSelection(0, 2, 0, 5))

        // Drag the START handle past the end — a legitimate gesture in any
        // Android text field.
        controller.arrastarAlca(SelectionHandle.INICIO, Offset(170f, 10f))

        val cruzada = holder.selection!!
        assertEquals(GridSelection(0, 8, 0, 5), cruzada)
        // Crossed in the data, ordered when it comes to measuring and drawing.
        assertEquals(Rect(100f, 0f, 180f, 40f), selectionBounds(cruzada, hitTester))
    }

    @Test
    fun arrastarAlcaSemSelecao_naoCriaSelecaoDoNada() {
        val holder = GridSelectionHolder()
        val controller = SelectionGestureController({ hitTester }, holder)

        controller.arrastarAlca(SelectionHandle.FIM, Offset(50f, 50f))

        assertNull(holder.selection)
    }

    @Test
    fun oRetanguloDaSelecaoEoQueAbarraFlutuanteRecebeParaSePosicionar() {
        // Without this rectangle the system would use the bounds of the whole
        // view and the bar would land at the top of the screen, far from what
        // was selected.
        val selecao = GridSelection(startRow = 1, startCol = 2, endRow = 1, endCol = 4)

        assertEquals(Rect(40f, 40f, 100f, 80f), selectionBounds(selecao, hitTester))
    }

    @Test
    fun oRealceDeVariasLinhas_pegaAsLinhasDoMeioInteiras() {
        val faixas = selectionRowRanges(GridSelection(0, 7, 2, 3), cols = 10)

        assertEquals(listOf(7..9, 0..9, 0..3), faixas)
    }

    @Test
    fun oRealceDeUmaLinhaSo_ficaEntreAsDuasColunas() {
        assertEquals(listOf(2..6), selectionRowRanges(GridSelection(4, 2, 4, 6), cols = 10))
    }

    @Test
    fun aMudancaDeSelecaoAvisaQuemDesenhaAbarra() {
        // The floating bar belongs to the system: nothing observes the holder
        // on its own, it has to be CALLED. Without this notice the selection
        // would exist with no bar.
        val avisos = mutableListOf<GridSelection?>()
        val holder = GridSelectionHolder()
        val controller = SelectionGestureController({ hitTester }, holder) { avisos += it }

        controller.definirSelecao(GridSelection(0, 0, 0, 2))
        controller.arrastarAlca(SelectionHandle.FIM, Offset(90f, 10f))
        controller.clearSelection()

        assertEquals(3, avisos.size)
        assertNull("o último aviso é o de que não há mais seleção", avisos.last())
    }

    @Test
    fun limparUmaSelecaoQueJaNaoExiste_naoAvisaDeNovo() {
        // Closing the bar clears the selection, and clearing the selection
        // closes the bar. Without this guard the pair would loop.
        val avisos = mutableListOf<GridSelection?>()
        val holder = GridSelectionHolder()
        val controller = SelectionGestureController({ hitTester }, holder) { avisos += it }

        controller.clearSelection()

        assertEquals(0, avisos.size)
    }
}
