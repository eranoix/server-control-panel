package dev.servercontrolpanel.feature.terminal.selection

import dev.servercontrolpanel.terminalengine.CellSnapshot

internal fun extractSelectedText(snapshot: CellSnapshot, selection: GridSelection): String {
    val normalized = inReadingOrder(selection)
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

internal class SelectedText(
    private val snapshotProvider: () -> CellSnapshot?,
    private val selectionProvider: () -> GridSelection?,
) {
    fun read(): String? {
        val snapshot = snapshotProvider() ?: return null
        val selection = selectionProvider() ?: return null
        return extractSelectedText(snapshot, selection)
    }

    fun use(consume: (String) -> Unit) {
        val content = read()
        if (content.isNullOrEmpty()) return
        consume(content)
    }
}

internal class CopyAction(
    private val snapshotProvider: () -> CellSnapshot?,
    private val selectionProvider: () -> GridSelection?,
    private val clipboardWrite: (String) -> Unit,
) {
    private val text = SelectedText(snapshotProvider, selectionProvider)

    fun copy() {
        clipboardWrite(text.read() ?: return)
    }
}

internal class PasteAction(
    private val clipboardRead: () -> String?,
    private val sendPaste: (String) -> Unit,
    private val onContentMissing: () -> Unit = {},
) {
    fun paste() {
        val text = clipboardRead()
        if (text.isNullOrEmpty()) {
            onContentMissing()
            return
        }
        sendPaste(text)
    }
}
