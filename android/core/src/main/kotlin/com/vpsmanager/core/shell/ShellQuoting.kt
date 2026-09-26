package com.vpsmanager.core.shell

/**
 * Turns a file path from the server into a shell ARGUMENT — the last step of
 * attaching through the terminal, and the only one that can break silently.
 *
 * **Why this exists.** The app uploads a file, the server returns the final
 * path (`/opt/panel/data/mobile-inbox/Screen shot.png`) and that path is
 * inserted into the command line of a live session. Inserted raw, the shell
 * splits it on the space: `cat` receives three arguments (`.../Screen`,
 * `shot.png`, and so on) and answers with "No such file or directory" for each.
 * A path containing `$`, a backtick or `;` is worse than confusing — it is
 * EXECUTION: `$(...)` and backticks run a command, `;` chains another. Since
 * the file name comes from the device (the gallery, a file manager, a name the
 * user typed themselves), it is untrusted input to the shell even when it is
 * harmless to the server.
 *
 * **The rule.** It is the same one as Python's `shlex.quote` and `printf %q` —
 * not a local invention: if the text is made up only of characters that EVERY
 * POSIX shell treats literally, it goes out as it is (the common case, and what
 * keeps the command line readable); any other character forces SINGLE quotes
 * around it. Single quotes are the right choice over double ones because inside
 * them the shell interprets absolutely nothing — not `$`, not a backtick, not
 * the backslash — so there is no second layer of escaping to get wrong. The one
 * thing that cannot appear inside single quotes is the single quote itself, and
 * it is handled the canonical way: close, splice in an escaped quote, reopen
 * (`'\''`).
 */

/**
 * The set of characters that needs no quoting. Deliberately an ALLOW LIST,
 * never a deny list: a shell has far too many metacharacters (`|`, `&`, `;`,
 * `<`, `>`, `(`, `)`, `$`, backtick, backslash, quotes, tab, newline, `*`, `?`,
 * `[`, `#`, `~`, `!`, plus the space) for forgetting one to be unlikely — and
 * forgetting one means command injection, not a cosmetic bug. With an allow
 * list, the possible mistake is the harmless one: quoting where it was not
 * needed.
 */
private val CARACTERES_SEM_ASPAS = Regex("^[A-Za-z0-9_@%+=:,./-]+$")

/**
 * Returns [texto] ready to be inserted as ONE shell argument.
 *
 * Empty text becomes `''` — without that, "nothing" would disappear from the
 * command line instead of becoming a genuinely empty argument.
 */
fun comAspasParaShell(texto: String): String {
    if (texto.isEmpty()) return "''"
    if (CARACTERES_SEM_ASPAS.matches(texto)) return texto
    // Close the quote, escape the literal quote outside it, reopen. It is the
    // canonical form and works in sh/bash/zsh/dash alike.
    return "'" + texto.replace("'", "'\\''") + "'"
}

/**
 * Assembles the text to be inserted into the command line for [caminhos].
 *
 * Two decisions that matter more than the code:
 *
 * 1. **It ends with a space, never with a newline.** A `\n` would EXECUTE the
 *    line on the spot — inserting a reference must never fire a command the
 *    operator has not finished writing. The trailing space is the opposite: it
 *    leaves the cursor ready for the next argument, which is exactly what you
 *    do after attaching (`cat <path> ` → carry on typing).
 * 2. **Several paths go in separated by spaces, each with its own quoting.**
 *    It is the form an `ls`/`cat`/`file` already understands, without the
 *    operator having to insert them one at a time.
 */
fun textoDeInsercaoParaShell(caminhos: List<String>): String {
    if (caminhos.isEmpty()) return ""
    return caminhos.joinToString(separator = " ", postfix = " ") { comAspasParaShell(it) }
}
