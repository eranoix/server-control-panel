package com.vpsmanager.feature.terminal.selection

import com.vpsmanager.terminalengine.CellSnapshot

/**
 * Walks [snapshot]'s cells for [selection]'s range and rebuilds exactly what
 * was visibly printed there — this is the literal acceptance text: "no
 * padding, no lost line breaks."
 *
 * Two silent-corruption risks this deliberately avoids:
 * - **Trailing padding:** a cell the renderer never wrote to carries
 *   codepoint `0` (see [com.vpsmanager.feature.terminal.render.buildRowDrawOps]'s
 *   own doc comment — the same convention this reads). Those trailing
 *   never-written cells are trimmed from each row's end; a REAL trailing
 *   space the shell printed (codepoint `0x20`) is content and is kept,
 *   because "never written" and "printed a space" are different codepoints
 *   upstream and this function never conflates them.
 * - **Lost/duplicated line breaks:** [CellSnapshot.isWrapped] marks a row
 *   that keeps going onto the next row (a soft wrap from filling the fixed
 *   grid width) — no `\n` is inserted after it, so a long line the terminal
 *   wrapped for display reproduces as ONE logical line. Every other row
 *   boundary in the selection gets exactly one `\n`.
 *
 * A [CellSnapshot.Wide.SPACER_TAIL]/[CellSnapshot.Wide.SPACER_HEAD] cell
 * contributes nothing (same convention `buildRowDrawOps` uses): the
 * [CellSnapshot.Wide.WIDE] cell alone carries the glyph's codepoint, so a
 * double-width character copies as exactly one character, never two.
 */
internal fun extractSelectedText(snapshot: CellSnapshot, selection: GridSelection): String {
    // One single normalisation across the whole module — the same one that
    // positions the handles and measures the floating bar's rectangle (see
    // `SelectionHandles.kt`).
    val normalized = emOrdemDeLeitura(selection)
    val sb = StringBuilder()
    for (row in normalized.startRow..normalized.endRow) {
        val fromCol = if (row == normalized.startRow) normalized.startCol else 0
        val toCol = if (row == normalized.endRow) normalized.endCol else snapshot.cols - 1
        sb.append(extractRow(snapshot, row, fromCol, toCol))
        if (row != normalized.endRow && !snapshot.isWrapped(row)) {
            sb.append('\n')
        }
    }
    return sb.toString()
}

private data class RowChar(val codepoint: Int, val isPadding: Boolean)

private fun extractRow(snapshot: CellSnapshot, row: Int, fromCol: Int, toCol: Int): String {
    val chars = ArrayList<RowChar>(toCol - fromCol + 1)
    for (col in fromCol..toCol) {
        val cell = snapshot.cellAt(col, row)
        if (cell.wide == CellSnapshot.Wide.SPACER_TAIL || cell.wide == CellSnapshot.Wide.SPACER_HEAD) continue
        chars += if (cell.codepoint == 0) RowChar(' '.code, isPadding = true) else RowChar(cell.codepoint, isPadding = false)
    }
    var contentEnd = chars.size
    while (contentEnd > 0 && chars[contentEnd - 1].isPadding) contentEnd--
    val sb = StringBuilder()
    for (i in 0 until contentEnd) sb.appendCodePoint(chars[i].codepoint)
    return sb.toString()
}

/**
 * The text under the selection RIGHT NOW — read from the live snapshot at the
 * instant of the click, never from a copy kept when the bar appeared.
 *
 * It exists as an object of its own because three of the bar's actions need
 * exactly this and the same two guards (no snapshot, no selection): copy,
 * share, and send back to the remote program. Duplicating the guards is how
 * the first version's "buttons that do nothing" were born.
 */
internal class TextoSelecionado(
    private val snapshotProvider: () -> CellSnapshot?,
    private val selectionProvider: () -> GridSelection?,
) {
    /** The reconstructed text, or `null` if the snapshot or the selection is missing. */
    fun ler(): String? {
        val snapshot = snapshotProvider() ?: return null
        val selection = selectionProvider() ?: return null
        return extractSelectedText(snapshot, selection)
    }

    /**
     * Hands the selected text to [consumir], and calls NOTHING when there is
     * no text — it is the single guard for the overflow menu's actions (share,
     * send back to the program, open in another app).
     *
     * Opening a share sheet with empty text, or sending zero bytes to the
     * shell, are two different ways for a tap to look broken; and each action
     * repeating its own guard is how the first version's "buttons that do
     * nothing" were born.
     */
    fun usar(consumir: (String) -> Unit) {
        val conteudo = ler()
        if (conteudo.isNullOrEmpty()) return
        consumir(conteudo)
    }
}

/**
 * The "Copy" side of the system's floating bar: reads the live snapshot and
 * the selection once and writes the reconstructed text to Android's clipboard
 * via [clipboardWrite] — the same clipboard as any other app, not an internal
 * buffer.
 *
 * It does nothing when the snapshot or the selection is missing, rather than
 * writing an empty entry over whatever the user had copied elsewhere.
 */
internal class CopyAction(
    private val snapshotProvider: () -> CellSnapshot?,
    private val selectionProvider: () -> GridSelection?,
    private val clipboardWrite: (String) -> Unit,
) {
    private val texto = TextoSelecionado(snapshotProvider, selectionProvider)

    fun copy() {
        clipboardWrite(texto.ler() ?: return)
    }
}

/**
 * The "Paste" side: the clipboard's entire contents go to [sendPaste] in
 * EXACTLY one call — never in pieces, never simulated as key-by-key typing,
 * which would lose the atomicity that bracketed paste (DECSET 2004) exists to
 * guarantee.
 *
 * Whether the text is wrapped in `ESC[200~`/`ESC[201~` is decided by the VT
 * emulator, from the mode the remote program turned on (see
 * `TerminalEngine.encodePaste`). Pasting into a shell without that wrapper
 * gets multi-line text EXECUTED on the spot; wrapping when the program did not
 * ask dumps the markers as rubbish on the command line. Both bugs existed.
 *
 * An empty (or absent) clipboard sends nothing: a zero-length `paste` has no
 * useful effect on the remote shell. But it is not silent either — it calls
 * [aoFaltarConteudo].
 *
 * **Why telling the user matters here.** "Paste" is pinned to the bar, at the
 * app owner's request, and it is the only action there that may have nothing
 * to do: the others act on the selection, which by definition exists for as
 * long as the bar is up. The item used to be hidden when there was nothing
 * copied — which made the bar change shape between one use and the next, and
 * still triggered Android 12's clipboard-read notice just to decide that (see
 * `TerminalActionMode.onPrepareActionMode`). We swapped that: the button stays
 * in the same place always, and when there is nothing to paste it SAYS so.
 */
internal class PasteAction(
    private val clipboardRead: () -> String?,
    private val sendPaste: (String) -> Unit,
    private val aoFaltarConteudo: () -> Unit = {},
) {
    fun paste() {
        val text = clipboardRead()
        if (text.isNullOrEmpty()) {
            aoFaltarConteudo()
            return
        }
        sendPaste(text)
    }
}
