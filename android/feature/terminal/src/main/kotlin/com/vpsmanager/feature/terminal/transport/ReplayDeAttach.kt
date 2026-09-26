package com.vpsmanager.feature.terminal.transport

/**
 * Decides what to do with the scrollback block the server re-emits on a FRESH
 * attach — the "replay".
 *
 * ## The defect this fixes
 *
 * The server (`internal/pty/pty.go` → `attachReplay`) keeps a tee of everything
 * the PTY writes and, on a new attach, hands back the last 128 KiB of that file
 * as ONE binary message, before wiring up the proxy. For an ordinary shell that
 * is exactly right: a shell's output is append-only, and re-emitting the tail
 * repaints the history.
 *
 * For a program that redraws — Claude Code, which uses the Ink renderer and
 * does NOT enter alt-screen, and so escapes the only guard the server has today
 * (`sessionInAltScreen`) — that same tail is poison, for two independent
 * reasons, both measured:
 *
 * 1. **Width.** The tail's text is already WRAPPED at whatever width the PTY
 *    had when it was produced. The real log of the "Aplicativo" session held
 *    frames recorded at 49, 24, 67, 77 and 113 columns. Replayed into a
 *    54-column grid, the 49-column frame shows up squeezed, with a fifth of the
 *    screen empty — and NO terminal emulator can undo that, because the breaks
 *    are `\r\n` the program itself emitted, not folds made by the terminal.
 *    Joining those lines would corrupt tables, `ls -l` and diffs.
 *
 * 2. **Duplication.** The tail does not hold ONE frame: it holds dozens. Ink
 *    redraws by moving the cursor up (`ESC[nA`) and repainting over the top,
 *    which only works relative to the LIVE cursor position. On an attach the
 *    cursor starts at the top of an empty grid, and `ESC[nA` saturates at the
 *    first line of the SCREEN — it cannot reach the scrollback. The previous
 *    frame has already scrolled up and stays there; the new one is painted
 *    below it. The same block of conversation shows up twice, three times,
 *    many times.
 *
 * ## Why "discard" is the right answer, and not "repair"
 *
 * On the READING side there is no repair: reprocessing the tail would require
 * knowing, line by line, whether a break came from the terminal (a fold, so
 * reflowable) or from the program (a hard break, untouchable) — and that
 * information is NOT recorded. It is the same reason no terminal reflows text a
 * program has already wrapped.
 *
 * What the operator wants to see — the current conversation, at the width of
 * their own screen — comes from the app's own primer
 * (`TerminalViewModel.iniciarPrimer`), which fetches the history and replays it
 * into libghostty-vt. The SERVER's replay would only precede that with an old,
 * crooked copy.
 *
 * ## AND ON THE WRITING SIDE A REPAIR CAME TO EXIST
 *
 * This KDoc used to say, without the caveat above, that "there is no repair".
 * The sentence was right about the tail and wrong about the problem: the server
 * now keeps a LIVE emulator at the session's grid, fed by the permanent
 * recorder, and pours into it the lines that LEAVE the screen. A line that has
 * scrolled out is finished — the program never touches it again — so it becomes
 * append-only text, with no cursor to saturate and no stale width to drag along.
 *
 * That is why `iniciarPrimer` asks `/terminal/historico` FIRST and only falls
 * back to `/terminal/log-bruto` when the session still has no history file.
 * Measured: 4096 KiB of raw log yield 360 KiB of history — 11.4x more
 * conversation per byte on the same network budget.
 *
 * THIS PARAGRAPH USED TO SAY SOMETHING ELSE: that the current frame came from
 * the server's "repaint wobble" — halving the PTY's line count and restoring
 * it, forcing the program to repaint. That was wrong, and expensive. The wobble
 * made the program lay out for 24 lines, paint, lay out for 48 and paint again,
 * compositing into the SAME buffer with blanks treated as transparent: the two
 * frames fused cell by cell. The result is there in the server's raw bytes
 * (`Aplicativo.log`, offset 2504030) — `A1gavetacsempreanavegou` where the text
 * is "A gaveta sempre navegou". Since it fired on every FRESH attach, leaving
 * the screen and coming back reapplied the damage, which is exactly what the
 * owner reported. The server no longer does that for a client that sends
 * `replay=0`.
 *
 * Older history stays reachable, and by a path that does not lie: the "load
 * older" panel in the options sheet, which fetches the server's log as text.
 *
 * ## The criterion, and how it was calibrated
 *
 * A shell is append-only: it never moves the cursor up to rewrite what has
 * already gone out. A differential renderer does that all the time. So the
 * signature is the density of CUU (`ESC[<n>A`) with `n >= 2` — going up TWO or
 * more lines only makes sense for something rewriting a block.
 *
 * `n == 1` is left out on purpose: it is what bash's `readline` emits to redraw
 * a two-line prompt, and counting it would classify an ordinary shell as a TUI.
 * Measured across the 30 real session logs on this machine, over the same
 * 128 KiB window the server sends:
 *
 * ```
 *   shells         : 0, 0, 0, 0, 0, 3, 11        (max 11)
 *   Claude Code    : 33, 52, 125, 396, ... 2448  (min 33)
 * ```
 *
 * The gap between 11 and 33 is empty, and [LIMITE_REPINTURA] = 20 sits in the
 * middle of it. Erring towards "it is a shell" is the cheap side: at worst the
 * operator sees the history the way they always have.
 */
object ReplayDeAttach {

    /**
     * How many two-or-more-line CUUs it takes to call a stream "redrawn". See
     * the calibration in the class comment: it sits in the measured gap
     * between the worst shell (11) and the best TUI (33).
     */
    const val LIMITE_REPINTURA: Int = 20

    /**
     * Was this stream produced by a differential renderer (Ink, `less`,
     * anything that repaints by moving the cursor up)?
     *
     * Scans the bytes once, without allocating: the block can be up to 128 KiB
     * and this runs on the WebSocket thread, at the moment of the attach.
     */
    fun ehRepinturaDiferencial(bytes: ByteArray): Boolean = contarCuuDeBloco(bytes) >= LIMITE_REPINTURA

    /**
     * How many times the stream moves the cursor up TWO or more lines —
     * `ESC [ n A` with `n >= 2`.
     *
     * `ESC[A` with no number means 1 by definition of the standard, so it does
     * not count. An empty parameter or `0` also mean 1 (ECMA-48: an omitted or
     * zero parameter takes the command's default value, which here is 1).
     */
    internal fun contarCuuDeBloco(bytes: ByteArray): Int {
        var total = 0
        var i = 0
        val fim = bytes.size
        while (i < fim - 1) {
            if (bytes[i] == ESC && bytes[i + 1] == COLCHETE) {
                var j = i + 2
                var valor = 0
                var digitos = 0
                while (j < fim && bytes[j] >= ZERO && bytes[j] <= NOVE) {
                    // Saturates instead of overflowing: anything above 2 has
                    // already decided the test, and an absurdly long parameter
                    // in a corrupted stream must not become an arithmetic
                    // overflow.
                    if (valor < 1000) valor = valor * 10 + (bytes[j] - ZERO)
                    digitos += 1
                    j += 1
                }
                if (j < fim && bytes[j] == CUU_FINAL) {
                    val linhas = if (digitos == 0 || valor == 0) 1 else valor
                    if (linhas >= 2) total += 1
                    i = j + 1
                    continue
                }
            }
            i += 1
        }
        return total
    }

    private const val ESC: Byte = 0x1B
    private const val COLCHETE: Byte = '['.code.toByte()
    private const val ZERO: Byte = '0'.code.toByte()
    private const val NOVE: Byte = '9'.code.toByte()
    private const val CUU_FINAL: Byte = 'A'.code.toByte()
}
