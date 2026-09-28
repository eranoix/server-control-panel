package dev.servercontrolpanel.core.shell

private val UNQUOTED_CHARS = Regex("^[A-Za-z0-9_@%+=:,./-]+$")

fun shellQuoted(text: String): String {
    if (text.isEmpty()) return "''"
    if (UNQUOTED_CHARS.matches(text)) return text
    return "'" + text.replace("'", "'\\''") + "'"
}

fun shellInsertionText(paths: List<String>): String {
    if (paths.isEmpty()) return ""
    return paths.joinToString(separator = " ", postfix = " ") { shellQuoted(it) }
}
