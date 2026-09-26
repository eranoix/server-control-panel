package com.vpsmanager.feature.terminal.selection

import com.vpsmanager.terminalengine.CellSnapshot
import org.junit.Assert.assertEquals
import org.junit.Test

/** Writes [texto] from column 0 of row [linha]; the rest stays "never written" (codepoint 0). */
private fun grade(
    cols: Int,
    rows: Int,
    rowFlags: ByteArray = ByteArray(rows),
    vararg linhas: String,
): CellSnapshot = buildSnapshot(cols = cols, rows = rows, rowFlags = rowFlags) { row, col ->
    val texto = linhas.getOrNull(row) ?: ""
    narrowCell(if (col < texto.length) texto[col].code else 0)
}

/**
 * Double-tap (word) and triple-tap (line) selection — the idiom every Android
 * text field speaks and the app's terminal did not.
 *
 * Before this, the only way to select was a long press followed by a drag,
 * cell by cell: to copy a filename the operator had to aim at the first letter
 * and drag to the last, with no highlight on screen to check against (the
 * `TerminalCanvas` never drew a selection).
 */
class WordSelectionTest {

    // ---- Word ---------------------------------------------------------

    @Test
    fun toqueNoMeioDeUmaPalavra_selecionaAPalavraInteira() {
        val snapshot = grade(cols = 24, rows = 1, linhas = arrayOf("git commit --amend"))

        // The finger lands on the "m" of "commit" (columns 6..11).
        val selecao = selecionarPalavra(snapshot, row = 0, col = 8)

        assertEquals(GridSelection(0, 4, 0, 9), selecao)
        assertEquals("commit", extractSelectedText(snapshot, selecao))
    }

    @Test
    fun aPalavraNaoAtravessaOespacoVizinho() {
        val snapshot = grade(cols = 24, rows = 1, linhas = arrayOf("git commit --amend"))

        val primeira = selecionarPalavra(snapshot, row = 0, col = 0)

        assertEquals("git", extractSelectedText(snapshot, primeira))
    }

    @Test
    fun sublinhadoEparteDaPalavra_masOhifenNao() {
        // A `_` in the middle of an identifier is content; a `-` separates a
        // flag from its name, and breaking there is what makes the double tap
        // useful on `--amend`.
        val snapshot = grade(cols = 32, rows = 1, linhas = arrayOf("VPS_MANAGER_HOME --dry-run"))

        assertEquals("VPS_MANAGER_HOME", extractSelectedText(snapshot, selecionarPalavra(snapshot, 0, 5)))
        assertEquals("dry", extractSelectedText(snapshot, selecionarPalavra(snapshot, 0, 20)))
    }

    @Test
    fun pontuacaoAgrupaComPontuacao() {
        // `--` is a single block: two characters of the same class. Without
        // this, double-tapping a `--` would select a lone dash.
        val snapshot = grade(cols = 16, rows = 1, linhas = arrayOf("ls --all"))

        assertEquals("--", extractSelectedText(snapshot, selecionarPalavra(snapshot, 0, 3)))
    }

    @Test
    fun toqueNumEspaco_selecionaOblocoDeEspacos_naoNada() {
        // One pixel beside the word must not turn the gesture into nothing:
        // that would be a double tap that "sometimes doesn't work".
        val snapshot = grade(cols = 16, rows = 1, linhas = arrayOf("ab    cd"))

        val selecao = selecionarPalavra(snapshot, row = 0, col = 3)

        assertEquals(GridSelection(0, 2, 0, 5), selecao)
    }

    @Test
    fun celulaNuncaEscritaContaComoEspaco() {
        // The tail of the row is renderer padding (codepoint 0), not text.
        val snapshot = grade(cols = 10, rows = 1, linhas = arrayOf("ab"))

        val selecao = selecionarPalavra(snapshot, row = 0, col = 7)

        assertEquals(GridSelection(0, 2, 0, 9), selecao)
        assertEquals("o padding não vira texto ao ser copiado", "", extractSelectedText(snapshot, selecao))
    }

    @Test
    fun caractereLargo_naoQuebraApalavraAoMeio() {
        // A CJK character occupies two cells: the second is SPACER_TAIL and
        // has no codepoint of its own. Classifying it in isolation would split
        // the word.
        val snapshot = buildSnapshot(cols = 6, rows = 1) { _, col ->
            when (col) {
                0 -> wideCell('世'.code)
                1 -> narrowCell(0).copy(wide = CellSnapshot.Wide.SPACER_TAIL)
                2 -> wideCell('界'.code)
                3 -> narrowCell(0).copy(wide = CellSnapshot.Wide.SPACER_TAIL)
                else -> narrowCell(' '.code)
            }
        }

        val selecao = selecionarPalavra(snapshot, row = 0, col = 0)

        assertEquals(GridSelection(0, 0, 0, 3), selecao)
        assertEquals("世界", extractSelectedText(snapshot, selecao))
    }

    // ---- Line -----------------------------------------------------------

    @Test
    fun toqueTriplo_selecionaAlinhaAteOultimoCaractereEscrito() {
        val snapshot = grade(cols = 20, rows = 2, linhas = arrayOf("primeira", "segunda"))

        val selecao = selecionarLinha(snapshot, row = 1)

        assertEquals(GridSelection(1, 0, 1, 6), selecao)
        assertEquals("segunda", extractSelectedText(snapshot, selecao))
    }

    @Test
    fun toqueTriplo_pegaAlinhaLOGICAinteiraQuandoOterminalQuebrouOtexto() {
        // A long line that did not fit the grid's width occupies three SCREEN
        // rows. Selecting only the visible run would hand back a path cut in
        // half — the classic defect of copying from a terminal.
        val flags = byteArrayOf(0x01, 0x03, 0x02, 0x00)
        val snapshot = grade(
            cols = 8,
            rows = 4,
            rowFlags = flags,
            linhas = arrayOf("/opt/vps", "-manager", "/bin", "outra"),
        )

        val selecao = selecionarLinha(snapshot, row = 1)

        assertEquals(GridSelection(0, 0, 2, 3), selecao)
        assertEquals(
            "a quebra suave não pode virar quebra de linha no texto copiado",
            "/opt/panel/bin",
            extractSelectedText(snapshot, selecao),
        )
    }

    @Test
    fun linhaEmBranco_daUmaSelecaoValidaEvazia() {
        val snapshot = grade(cols = 8, rows = 2, linhas = arrayOf("algo", ""))

        val selecao = selecionarLinha(snapshot, row = 1)

        assertEquals(GridSelection(1, 0, 1, 0), selecao)
        assertEquals("", extractSelectedText(snapshot, selecao))
    }

    // ---- Select all -------------------------------------------------

    @Test
    fun selecionarTudo_cobreAgradeInteira() {
        val snapshot = grade(cols = 6, rows = 3, linhas = arrayOf("um", "dois", "tres"))

        val selecao = selecionarTudo(snapshot)

        assertEquals(GridSelection(0, 0, 2, 5), selecao)
        assertEquals("um\ndois\ntres", extractSelectedText(snapshot, selecao))
    }
}
