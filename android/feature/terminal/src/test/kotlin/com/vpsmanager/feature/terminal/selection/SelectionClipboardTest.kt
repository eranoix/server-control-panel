package com.vpsmanager.feature.terminal.selection

import com.vpsmanager.terminalengine.CellSnapshot
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test

/** Never-written cell: codepoint 0, narrow — the padding convention `RowDrawOps` also reads. */
private fun blankCell(): CellSnapshot.Cell = narrowCell(0)

private const val WRAPPED_FLAG = 0x01

class SelectionClipboardTest {

    @Test
    fun extractSelectedText_wrappedRow_joinsWithoutInsertingALineBreak() {
        // Row 0 is "abcde" and soft-wraps into row 1 "fghij" -- one logical
        // line split by the fixed 5-col width, not two real lines.
        val snapshot = buildSnapshot(
            cols = 5,
            rows = 2,
            rowFlags = byteArrayOf(WRAPPED_FLAG.toByte(), 0),
        ) { row, col ->
            val text = if (row == 0) "abcde" else "fghij"
            narrowCell(text[col].code)
        }

        val text = extractSelectedText(snapshot, GridSelection(startRow = 0, startCol = 0, endRow = 1, endCol = 4))

        assertEquals("abcdefghij", text)
    }

    @Test
    fun extractSelectedText_genuineNewline_isPreservedBetweenNonWrappedRows() {
        val snapshot = buildSnapshot(cols = 3, rows = 2) { row, col ->
            val text = if (row == 0) "abc" else "def"
            narrowCell(text[col].code)
        }

        val text = extractSelectedText(snapshot, GridSelection(startRow = 0, startCol = 0, endRow = 1, endCol = 2))

        assertEquals("abc\ndef", text)
    }

    @Test
    fun extractSelectedText_shortRow_trimsOnlyTrailingNeverWrittenPadding() {
        // "hi" printed, then the rest of the row was never written (codepoint 0).
        val snapshot = buildSnapshot(cols = 5, rows = 1) { _, col ->
            when (col) {
                0 -> narrowCell('h'.code)
                1 -> narrowCell('i'.code)
                else -> blankCell()
            }
        }

        val text = extractSelectedText(snapshot, GridSelection(startRow = 0, startCol = 0, endRow = 0, endCol = 4))

        assertEquals("hi", text)
    }

    @Test
    fun extractSelectedText_genuineTrailingSpace_isKeptNotTrimmed() {
        // "hi " -- the shell actually printed a real 0x20 space as the last
        // character, followed by never-written padding. Only the padding trims.
        val snapshot = buildSnapshot(cols = 5, rows = 1) { _, col ->
            when (col) {
                0 -> narrowCell('h'.code)
                1 -> narrowCell('i'.code)
                2 -> narrowCell(' '.code)
                else -> blankCell()
            }
        }

        val text = extractSelectedText(snapshot, GridSelection(startRow = 0, startCol = 0, endRow = 0, endCol = 4))

        assertEquals("hi ", text)
    }

    @Test
    fun extractSelectedText_doubleWidthGlyph_collapsesToOneCharacterNoExtraPadding() {
        // A CJK-style wide glyph at col 0 occupies col 0 (WIDE) + col 1 (SPACER_TAIL),
        // then "x" at col 2.
        val snapshot = buildSnapshot(cols = 3, rows = 1) { _, col ->
            when (col) {
                0 -> wideCell(0x4e2d) // 中
                1 -> spacerTailCell()
                else -> narrowCell('x'.code)
            }
        }

        val text = extractSelectedText(snapshot, GridSelection(startRow = 0, startCol = 0, endRow = 0, endCol = 2))

        assertEquals("中x", text)
        assertEquals("exactly 2 characters, never 3", 2, text.length)
    }

    @Test
    fun extractSelectedText_reversedDragDirection_stillReadsInForwardReadingOrder() {
        val snapshot = buildSnapshot(cols = 3, rows = 2) { row, col ->
            val text = if (row == 0) "abc" else "def"
            narrowCell(text[col].code)
        }

        // Dragged from bottom-right to top-left -- start/end are swapped versus reading order.
        val text = extractSelectedText(snapshot, GridSelection(startRow = 1, startCol = 2, endRow = 0, endCol = 0))

        assertEquals("abc\ndef", text)
    }

    @Test
    fun copyAction_noOpWhenNoSelection() {
        val snapshot = buildSnapshot(cols = 1, rows = 1) { _, _ -> narrowCell('a'.code) }
        val written = mutableListOf<String>()
        val action = CopyAction(snapshotProvider = { snapshot }, selectionProvider = { null }, clipboardWrite = { written += it })

        action.copy()

        assertTrue(written.isEmpty())
    }

    @Test
    fun copyAction_noOpWhenNoSnapshotYet() {
        val written = mutableListOf<String>()
        val action = CopyAction(
            snapshotProvider = { null },
            selectionProvider = { GridSelection(0, 0, 0, 0) },
            clipboardWrite = { written += it },
        )

        action.copy()

        assertTrue(written.isEmpty())
    }

    @Test
    fun copyAction_writesExactExtractedTextExactlyOnce() {
        val snapshot = buildSnapshot(cols = 3, rows = 1) { _, col -> narrowCell("abc"[col].code) }
        val written = mutableListOf<String>()
        val action = CopyAction(
            snapshotProvider = { snapshot },
            selectionProvider = { GridSelection(0, 0, 0, 2) },
            clipboardWrite = { written += it },
        )

        action.copy()

        assertEquals(listOf("abc"), written)
    }

    @Test
    fun pasteAction_emptyClipboard_neverCallsSendPaste() {
        var callCount = 0
        val action = PasteAction(clipboardRead = { "" }, sendPaste = { callCount++ })

        action.paste()

        assertEquals(0, callCount)
    }

    @Test
    fun pasteAction_nullClipboard_neverCallsSendPaste() {
        var callCount = 0
        val action = PasteAction(clipboardRead = { null }, sendPaste = { callCount++ })

        action.paste()

        assertEquals(0, callCount)
    }

    @Test
    fun pasteAction_multiParagraphClipboard_sendsExactlyOnceWithTheEntireContent() {
        val multiParagraph = "primeira linha\nsegunda linha\n\nquarto paragrafo com espaco final "
        var callCount = 0
        val sent = mutableListOf<String>()
        val action = PasteAction(
            clipboardRead = { multiParagraph },
            sendPaste = { callCount++; sent += it },
        )

        action.paste()

        assertEquals("exactly one sendPaste call, never chunked", 1, callCount)
        assertEquals(listOf(multiParagraph), sent)
    }

    // ---- An empty clipboard: the item pinned to the bar has to EXPLAIN ----
    //
    // "Paste" is always on the bar, so it is the only item that may have
    // nothing to do. It used to disappear from the bar in that case; now it
    // stays and says so.

    @Test
    fun pasteAction_areaDeTransferenciaVazia_avisaEmVezDeFicarEmSilencio() {
        var avisos = 0
        val action = PasteAction(
            clipboardRead = { "" },
            sendPaste = { fail("não pode mandar byte nenhum com a área vazia") },
            aoFaltarConteudo = { avisos++ },
        )

        action.paste()

        assertEquals("um aviso, exatamente", 1, avisos)
    }

    @Test
    fun pasteAction_areaDeTransferenciaAusente_avisaTambem() {
        var avisos = 0
        val action = PasteAction(
            clipboardRead = { null },
            sendPaste = { fail("não pode mandar byte nenhum sem área de transferência") },
            aoFaltarConteudo = { avisos++ },
        )

        action.paste()

        assertEquals(1, avisos)
    }

    @Test
    fun pasteAction_comConteudo_naoAvisa() {
        var avisos = 0
        val sent = mutableListOf<String>()
        val action = PasteAction(
            clipboardRead = { "ls -la" },
            sendPaste = { sent += it },
            aoFaltarConteudo = { avisos++ },
        )

        action.paste()

        assertEquals("aviso é só para o caso vazio", 0, avisos)
        assertEquals(listOf("ls -la"), sent)
    }

    // ---- The two overflow actions that consume the selection ----

    @Test
    fun usar_entregaExatamenteOTextoSobASelecao() {
        val snapshot = buildSnapshot(cols = 6, rows = 1) { _, col -> narrowCell("ls -la"[col].code) }
        val recebido = mutableListOf<String>()

        TextoSelecionado({ snapshot }, { GridSelection(0, 0, 0, 5) }).usar { recebido += it }

        assertEquals(listOf("ls -la"), recebido)
    }

    @Test
    fun usar_semSelecao_naoConsomeNada() {
        val snapshot = buildSnapshot(cols = 1, rows = 1) { _, _ -> narrowCell('a'.code) }

        TextoSelecionado({ snapshot }, { null }).usar {
            fail("sem seleção não há texto para compartilhar nem para reenviar")
        }
    }

    @Test
    fun usar_selecaoSoDeCelulasNuncaEscritas_naoAbreSeletorVazio() {
        // A blank run of grid extracts "" — opening a share sheet with empty
        // text, or sending zero bytes to the shell, is the tap looking broken.
        val snapshot = buildSnapshot(cols = 4, rows = 1) { _, _ -> narrowCell(0) }

        TextoSelecionado({ snapshot }, { GridSelection(0, 0, 0, 3) }).usar {
            fail("texto vazio não pode chegar ao consumidor")
        }
    }

    @Test
    fun textoSelecionado_leOSnapshotDoMomentoDoClique_naoUmaCopiaAntiga() {
        // The bar stays up while the remote program goes on printing; the
        // text handed over has to be what is on screen NOW.
        var atual = buildSnapshot(cols = 3, rows = 1) { _, col -> narrowCell("abc"[col].code) }
        val texto = TextoSelecionado({ atual }, { GridSelection(0, 0, 0, 2) })

        assertEquals("abc", texto.ler())
        atual = buildSnapshot(cols = 3, rows = 1) { _, col -> narrowCell("xyz"[col].code) }
        assertEquals("xyz", texto.ler())
    }
}
