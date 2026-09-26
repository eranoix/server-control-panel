package com.vpsmanager.feature.terminal.transport

/**
 * Decides what to do with the scrollback block the server re-emits on a fresh
 * attach (the "replay": the last 128 KiB of the PTY tee, see `attachReplay` in
 * `internal/pty/pty.go`).
 *
 * For an append-only shell the replay is right. For a differential renderer such
 * as Ink that does not use the alt screen, it is harmful:
 *
 * 1. Width: the text was already wrapped at whatever width the PTY had then
 *    (real logs mixed 24 to 113 columns), and those `\r\n` breaks cannot be
 *    reflowed without corrupting tables and diffs.
 * 2. Duplication: the tail holds dozens of frames drawn with relative `ESC[nA`
 *    moves, which saturate at the top of the screen on a fresh grid, so each
 *    frame lands below the previous one.
 *
 * The current conversation instead comes from `TerminalViewModel.startPrimer`,
 * which asks `/terminal/historico` first (the server feeds lines leaving the
 * screen of a live emulator into an append-only history; 4096 KiB of raw log
 * become 360 KiB of history) and falls back to `/terminal/log-bruto`. Older
 * history stays reachable through the "load older" panel.
 *
 * Criterion: shells never move the cursor up to rewrite; differential renderers
 * constantly do. The signature is the count of CUU (`ESC[<n>A`) with `n >= 2`
 * (`n == 1` is bash `readline` redrawing a two-line prompt). Measured over the
 * same 128 KiB window on 30 real logs:
 *
 * ```
 *   shells         : 0, 0, 0, 0, 0, 3, 11        (max 11)
 *   TUI sessions   : 33, 52, 125, 396, ... 2448  (min 33)
 * ```
 *
 * [REPAINT_THRESHOLD] = 20 sits in the gap; erring towards "shell" is the cheap side.
 */
object AttachReplay {

    /**
     * How many CUUs of two or more lines make a stream "redrawn"; sits in the
     * measured gap between the worst shell (11) and the best TUI (33).
     */
    const val REPAINT_THRESHOLD: Int = 20

    /**
     * Whether a differential renderer (Ink, `less`, anything that repaints by
     * moving the cursor up) produced this stream. One allocation-free pass, since
     * it runs on the WebSocket thread over up to 128 KiB.
     */
    fun isDiffRepaint(bytes: ByteArray): Boolean = countBlockCuu(bytes) >= REPAINT_THRESHOLD

    /**
     * How many times the stream moves the cursor up two or more lines
     * (`ESC [ n A`, `n >= 2`). A missing or zero parameter means 1 (ECMA-48).
     */
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
                    // Saturate instead of overflowing on a corrupted, very long parameter.
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
