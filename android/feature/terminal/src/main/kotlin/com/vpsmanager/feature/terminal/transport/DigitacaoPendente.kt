package com.vpsmanager.feature.terminal.transport

/**
 * What the person typed and has not yet gone to the server, as readable text.
 *
 * ## Why this has to exist
 *
 * In a terminal, **what you type only appears because the server sends the
 * echo back**. There is no local echo: the key becomes a byte, the byte goes
 * up, the shell answers with the same character, and only then is it drawn.
 * That is how every real terminal works, and it is what keeps a password from
 * appearing when the remote program turns the echo off.
 *
 * The consequence, when the connection drops, is the owner's exact complaint:
 * *"I press the keys and what I typed does not show up"*. The keys were not
 * lost — they are in `TerminalSocketClient`'s queue, and go out whole on
 * reconnect. But the screen goes mute, and a mute screen is indistinguishable
 * from a frozen app.
 *
 * ## Why it is NOT written into the grid
 *
 * The temptation is to inject the character straight into the emulator so it
 * "shows up right away". That would be wrong for two reasons that compound:
 *
 * 1. **It would appear twice.** When the queue drains, the server will echo
 *    those same bytes, and the real echo would add itself to the fake one.
 * 2. **It would desynchronise the emulator.** The grid is the exact replica of
 *    what the remote program drew; writing into it on our own account makes
 *    the local state diverge from the server, and from there on every cursor
 *    placement is wrong. It is the same class of defect as the blank screen in
 *    0.1.28.
 *
 * That is why the pending text lives in a STRIP, outside the grid. It does not
 * pretend the command is already in the shell — it shows what is being held to
 * go.
 */

/**
 * Appends [bytes] to [atual], translating them into something a person reads.
 *
 * The rules exist because what travels through here is not text: it is what a
 * terminal keyboard produces.
 *
 * - **Deleting really deletes.** Backspace (0x08) and DEL (0x7F) remove the
 *   last character of the summary. Showing "ls -laa⌫" would be worse than
 *   showing nothing: the person would read a command they are not going to
 *   send.
 * - **Enter becomes ⏎ and does not break the line.** The strip is one line
 *   tall; a real break would push the grid upwards with every queued command.
 * - **Escape sequences disappear.** An arrow key produces `ESC [ A` — three
 *   bytes that turn into illegible rubbish on screen. They are real and they
 *   do go to the server; what does not go to the strip is their drawing.
 * - **Ctrl becomes ^C.** It is the form every terminal already uses, and it is
 *   the information that matters: a `^C` in the queue changes what will happen
 *   on reconnect.
 */
internal fun resumoDaDigitacao(atual: String, bytes: ByteArray): String {
    val sb = StringBuilder(atual)
    var i = 0
    while (i < bytes.size) {
        val b = bytes[i].toInt() and 0xFF
        when {
            // ESC: swallow the whole sequence. The end of a CSI sequence is
            // the first letter after the parameters; for all the others, a
            // single byte.
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
                // Control: ^A..^Z and friends. `b + 64` is the matching
                // letter, which is exactly how a terminal already writes it.
                sb.append('^').append((b + 64).toChar())
                i++
            }
            b < 0x80 -> {
                sb.append(b.toChar())
                i++
            }
            else -> {
                // Multibyte UTF-8: work out the length from the first byte
                // and decode the whole piece at once. Byte by byte would
                // produce one replacement character per byte.
                val tamanho = when {
                    b and 0xE0 == 0xC0 -> 2
                    b and 0xF0 == 0xE0 -> 3
                    b and 0xF8 == 0xF0 -> 4
                    else -> 1
                }
                val fim = minOf(i + tamanho, bytes.size)
                sb.append(String(bytes, i, fim - i, Charsets.UTF_8))
                i = fim
            }
        }
    }
    // A ceiling so the strip does not turn into a paragraph: what matters is
    // the END, which is where the cursor is. Cutting from the front preserves
    // what the person has just typed.
    val texto = sb.toString()
    return if (texto.length <= MAXIMO_DO_RESUMO) {
        texto
    } else {
        "…" + texto.takeLast(MAXIMO_DO_RESUMO)
    }
}

/** Fits one strip line on a narrow phone, with room to spare for the label. */
private const val MAXIMO_DO_RESUMO = 120
