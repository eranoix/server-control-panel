package dev.servercontrolpanel.feature.terminal.transport

import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json

@Serializable
data class TerminalControlMessage(
    val type: String,
    val cols: Int? = null,
    val rows: Int? = null,
    val data: String? = null,
) {
    companion object {
        fun resize(cols: Int, rows: Int): TerminalControlMessage =
            TerminalControlMessage(type = "resize", cols = cols, rows = rows)

        private val json = Json { explicitNulls = false }

        fun sessionSize(text: String): Pair<Int, Int>? = runCatching {
            val m = json.decodeFromString(serializer(), text)
            if (m.type != "size") return null
            val c = m.cols ?: return null
            val r = m.rows ?: return null
            if (c < 1 || r < 1) return null
            c to r
        }.getOrNull()

        fun encode(message: TerminalControlMessage): String = json.encodeToString(serializer(), message)
    }
}
