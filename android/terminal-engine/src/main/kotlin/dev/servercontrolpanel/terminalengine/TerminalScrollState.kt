package dev.servercontrolpanel.terminalengine

data class TerminalScrollState(
    val total: Long,

    val offset: Long,

    val visible: Long,

    val atEnd: Boolean,
) {
    val history: Long get() = (total - visible).coerceAtLeast(0)

    val canScroll: Boolean get() = history > 0

    val progress: Float
        get() {
            val h = history
            if (h <= 0) return 1f
            return (offset.toFloat() / h.toFloat()).coerceIn(0f, 1f)
        }

    companion object {
        val AT_END = TerminalScrollState(total = 0, offset = 0, visible = 0, atEnd = true)
    }
}
