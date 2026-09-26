package com.vpsmanager.feature.terminal.transport

import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json

/**
 * Mirrors the Go wire shape exactly (`internal/pty/pty.go`'s `ctrlMsg`):
 *
 * ```go
 * type ctrlMsg struct {
 *     Type string `json:"type"`
 *     Cols uint16 `json:"cols,omitempty"`
 *     Rows uint16 `json:"rows,omitempty"`
 *     Data string `json:"data,omitempty"`
 * }
 * ```
 *
 * `explicitNulls = false` on [json] is this file's equivalent of Go's
 * `omitempty`: a `resize` message never carries a `data` key — same
 * convention already established for JSON-on-the-wire in `:core`'s
 * `SduiJson`. The `data` field stays in the format because it is part of the
 * wire contract; there simply is no message from this client that uses it any
 * more (see the note on pasting in `TerminalSocketClient`).
 */
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

        /**
         * Reads the announcement of the session's EFFECTIVE size that the
         * server sends (`{"type":"size","cols":N,"rows":M}`), or `null` if the
         * frame is not that.
         *
         * Tolerant on purpose: an unknown text frame must never bring the
         * terminal down nor turn into rubbish on the screen — it is simply not
         * this announcement.
         */
        fun tamanhoDaSessao(texto: String): Pair<Int, Int>? = runCatching {
            val m = json.decodeFromString(serializer(), texto)
            if (m.type != "size") return null
            val c = m.cols ?: return null
            val r = m.rows ?: return null
            if (c < 1 || r < 1) return null
            c to r
        }.getOrNull()

        /** Encodes as the exact text frame body sent over `/ws/shell`. */
        fun encode(message: TerminalControlMessage): String = json.encodeToString(serializer(), message)
    }
}
