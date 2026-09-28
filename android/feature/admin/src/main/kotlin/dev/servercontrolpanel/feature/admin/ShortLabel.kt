package dev.servercontrolpanel.feature.admin

internal fun shortLabel(label: String): String {
    val openParen = label.indexOf('(')
    if (openParen <= 0) return label.trim()
    val closeParen = label.indexOf(')', openParen)
    if (closeParen < 0) return label.trim()

    val inner = label.substring(openParen + 1, closeParen).trim()
    if (inner.length <= ACRONYM_LIMIT) return label.trim()

    val before = label.substring(0, openParen).trim()
    val after = label.substring(closeParen + 1).trim()
    val short = listOf(before, after).filter { it.isNotEmpty() }.joinToString(" ")
    return short.ifEmpty { label.trim() }
}

private const val ACRONYM_LIMIT = 4
