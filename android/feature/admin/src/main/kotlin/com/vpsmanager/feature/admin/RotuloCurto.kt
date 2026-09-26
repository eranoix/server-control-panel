package com.vpsmanager.feature.admin

/**
 * A section's label, shortened to fit inside a tile of the grid.
 *
 * ## What was wrong
 *
 * The owner sent a photo of the grid. One tile read:
 *
 * ```
 * Métricas (CPU,
 * memória, dis…
 * ```
 *
 * Two whole lines spent, and the truncation landed exactly on the part that
 * distinguishes. Worse: what survived was the DETAIL (`CPU, memória, dis…`)
 * and what was lost was the end of the name. The parenthesis exists to explain
 * in a wide menu; in a 110 dp tile it only pushes the name out.
 *
 * ## The rule
 *
 * Cut the parenthesis **when what is left still identifies**. `Métricas (CPU,
 * memória, disco)` becomes `Métricas` — nothing is lost, because the group
 * `Sistema` is written right below it.
 *
 * But `Firewall (UFW)` **keeps** its parenthesis: `UFW` is the name the tool
 * is known by, and the tag is short enough to fit. The cut is there to relieve
 * pressure, not to amputate.
 *
 * The threshold is the length of what sits inside the parentheses: up to four
 * characters it is an acronym (UFW, DNS, IA), and an acronym is dense
 * information; beyond that it is an enumeration, and an enumeration is what
 * does not fit.
 */
internal fun rotuloCurto(rotulo: String): String {
    val abre = rotulo.indexOf('(')
    if (abre <= 0) return rotulo.trim()
    val fecha = rotulo.indexOf(')', abre)
    if (fecha < 0) return rotulo.trim()

    val dentro = rotulo.substring(abre + 1, fecha).trim()
    if (dentro.length <= LIMITE_DE_SIGLA) return rotulo.trim()

    // Removes the parenthesised stretch and joins up what is left on either
    // side — there are sections whose parenthesis sits in the middle, not at
    // the end.
    val antes = rotulo.substring(0, abre).trim()
    val depois = rotulo.substring(fecha + 1).trim()
    val curto = listOf(antes, depois).filter { it.isNotEmpty() }.joinToString(" ")
    // If cutting would leave the label empty, the parenthesis WAS the name.
    // Hand back the original: a tile with no name is worse than a tile with a
    // truncated one.
    return curto.ifEmpty { rotulo.trim() }
}

/** Up to here it is an acronym (UFW, DNS, IA); beyond this it is an enumeration. */
private const val LIMITE_DE_SIGLA = 4
