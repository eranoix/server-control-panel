package com.vpsmanager.feature.terminal.selection

/**
 * Terminal-content selection expressed purely in grid cell coordinates
 * (row/col from the terminal-engine's cell snapshot), never pixel
 * coordinates or Android text-range offsets.
 *
 * This type deliberately has zero reference to `TerminalInputConnection`,
 * `BaseInputConnection`, `Editable` or `TextFieldValue`, and nothing under
 * `input/` references it back. Selection state and IME composition state are
 * two separate objects that neither reads nor writes the other — see
 * `TerminalInputConnectionTest` for the independence proof.
 */
data class GridSelection(
    val startRow: Int,
    val startCol: Int,
    val endRow: Int,
    val endCol: Int,
)

/**
 * The current selection, if any. A plain mutable holder — not a View, not an
 * Android text API — so tests (and later, gesture handling) can set it
 * without going anywhere near the IME.
 */
class GridSelectionHolder {
    var selection: GridSelection? = null
}
