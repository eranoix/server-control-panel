package dev.servercontrolpanel.feature.terminal.transport

internal fun typingSummary(current: String, bytes: ByteArray): String {
    val sb = StringBuilder(current)
    var i = 0
    while (i < bytes.size) {
        val b = bytes[i].toInt() and 0xFF
        when {
            b == 0x1B -> {
                i++
                if (i < bytes.size && (bytes[i].toInt() and 0xFF) == '['.code) {
                    i++
                    while (i < bytes.size) {
                        val c = bytes[i].toInt() and 0xFF
                        i++
                        if (c in 0x40..0x7E) break
                    }
                } else {
                    i++
                }
                continue
            }
            b == 0x08 || b == 0x7F -> {
                if (sb.isNotEmpty()) sb.deleteCharAt(sb.length - 1)
                i++
            }
            b == 0x0D || b == 0x0A -> {
                sb.append('⏎')
                i++
            }
            b < 0x20 -> {
                sb.append('^').append((b + 64).toChar())
                i++
            }
            b < 0x80 -> {
                sb.append(b.toChar())
                i++
            }
            else -> {
                val size = when {
                    b and 0xE0 == 0xC0 -> 2
                    b and 0xF0 == 0xE0 -> 3
                    b and 0xF8 == 0xF0 -> 4
                    else -> 1
                }
                val end = minOf(i + size, bytes.size)
                sb.append(String(bytes, i, end - i, Charsets.UTF_8))
                i = end
            }
        }
    }
    val text = sb.toString()
    return if (text.length <= SUMMARY_MAX) {
        text
    } else {
        "…" + text.takeLast(SUMMARY_MAX)
    }
}

private const val SUMMARY_MAX = 120
