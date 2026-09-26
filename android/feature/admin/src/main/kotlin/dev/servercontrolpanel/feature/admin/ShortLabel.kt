package dev.servercontrolpanel.feature.admin

/**
 * A section label shortened to fit a grid tile.
 *
 * A parenthetical longer than [ACRONYM_LIMIT] characters is an enumeration and
 * is dropped ("Metrics (CPU, memory, disk)" becomes "Metrics"); a short one is an
 * acronym worth keeping ("Firewall (UFW)").
 */
internal fun shortLabel(label: String): String {
    val openParen = label.indexOf('(')
    if (openParen <= 0) return label.trim()
    val closeParen = label.indexOf(')', openParen)
    if (closeParen < 0) return label.trim()

    val inner = label.substring(openParen + 1, closeParen).trim()
    if (inner.length <= ACRONYM_LIMIT) return label.trim()

    // Join both sides: the parenthetical may sit mid-label.
    val before = label.substring(0, openParen).trim()
    val after = label.substring(closeParen + 1).trim()
    val short = listOf(before, after).filter { it.isNotEmpty() }.joinToString(" ")
    // If nothing is left, the parenthetical was the name; keep the original.
    return short.ifEmpty { label.trim() }
}

/** Longest parenthetical treated as an acronym (UFW, DNS, AI). */
private const val ACRONYM_LIMIT = 4
