package dev.servercontrolpanel.feature.terminal.transport

object AttachReplay {

    const val REPAINT_THRESHOLD: Int = 20

    fun isDiffRepaint(bytes: ByteArray): Boolean = countBlockCuu(bytes) >= REPAINT_THRESHOLD

    internal fun countBlockCuu(bytes: ByteArray): Int {
        var total = 0
        var i = 0
        val end = bytes.size
        while (i < end - 1) {
            if (bytes[i] == ESC && bytes[i + 1] == BRACKET) {
                var j = i + 2
                var value = 0
                var digits = 0
                while (j < end && bytes[j] >= ZERO && bytes[j] <= NINE) {
                    if (value < 1000) value = value * 10 + (bytes[j] - ZERO)
                    digits += 1
                    j += 1
                }
                if (j < end && bytes[j] == CUU_FINAL) {
                    val lines = if (digits == 0 || value == 0) 1 else value
                    if (lines >= 2) total += 1
                    i = j + 1
                    continue
                }
            }
            i += 1
        }
        return total
    }

    private const val ESC: Byte = 0x1B
    private const val BRACKET: Byte = '['.code.toByte()
    private const val ZERO: Byte = '0'.code.toByte()
    private const val NINE: Byte = '9'.code.toByte()
    private const val CUU_FINAL: Byte = 'A'.code.toByte()
}
