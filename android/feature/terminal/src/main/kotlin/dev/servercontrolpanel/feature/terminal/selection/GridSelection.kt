package dev.servercontrolpanel.feature.terminal.selection

data class GridSelection(
    val startRow: Int,
    val startCol: Int,
    val endRow: Int,
    val endCol: Int,
)

class GridSelectionHolder {
    var selection: GridSelection? = null
}
