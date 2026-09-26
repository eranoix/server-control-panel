package com.vpsmanager.feature.terminal.geometria

/**
 * Where the terminal frame rests against the visible area.
 *
 * ## The defect
 *
 * The grid has as many rows as fit on screen — 52, on a tall device with a
 * small font. The frame the remote program draws is however tall IT wants: the
 * Claude Code composer, with its footer, takes about twenty. The remaining
 * thirty-two stay blank **at the bottom**, and the composer — the one place on
 * screen where anything gets done — stops in the top third, far from the thumb
 * and from the keyboard.
 *
 * It was the owner's report, in so many words: *"the writing window always has
 * to stay at the bottom"*.
 *
 * ## Why this is not a defect of the remote program
 *
 * An ordinary terminal never shows this because it is never taller than the
 * content already scrolled: text rises from the footer and the frame always
 * rests at the bottom. Here the grid is born large and filled by a replay, and
 * then grows (the connection banner disappears, the keyboard closes) — so blank
 * space at the bottom can exist, which on a desktop terminal it cannot. It is a
 * new condition, and the answer is ours to give.
 *
 * ## The rule
 *
 * The bottom of the CONTENT rests against the bottom of the VISIBLE AREA.
 * Nothing more:
 *
 * - frame shorter than the area → it moves down, and the blank goes to the top;
 * - frame taller than the area (keyboard up) → it moves up, exactly as it
 *   already did, showing the end;
 * - frame the same size as the area → nothing moves.
 *
 * One rule, in both directions, instead of a special case for the keyboard.
 */
object AncoraDoQuadro {

    /**
     * How far the frame has to shift in Y. Positive moves DOWN, negative UP.
     *
     * @param fundoDoConteudoPx where whatever is useful in the grid ends — see
     *   [ultimaLinhaUtil].
     * @param alturaVisivelPx the height the person actually sees.
     * @param maximoParaSubirPx how much grid exists beyond the visible area
     *   (what the keyboard covered). Moving up further than that would drag the
     *   grid off screen with nothing to put in its place.
     */
    fun deslocamentoY(fundoDoConteudoPx: Int, alturaVisivelPx: Int, maximoParaSubirPx: Int): Int =
        (alturaVisivelPx - fundoDoConteudoPx).coerceAtLeast(-maximoParaSubirPx)

    /**
     * The last row that has to stay visible.
     *
     * There are two questions, and the answer is the larger of the two:
     *
     * 1. **Where the drawn content ends** ([ultimaLinhaComConteudo]).
     * 2. **Where the cursor is**, with some slack below it. The slack is not
     *    decoration: in a TUI there is almost always content AFTER the cursor —
     *    in Claude Code the input box is three lines tall and the cursor sits on
     *    the middle one, so pinning the cursor to the edge ate the bottom border
     *    of the box. That too was the owner's report: *"the keyboard is cutting
     *    off the writing window"*.
     *
     * The second exists because the first is not enough when the cursor sits in
     * a region the drawing considers empty (a blank line inside the frame), and
     * the first exists because the second is not enough when there is a footer
     * below the cursor.
     */
    fun ultimaLinhaUtil(ultimaLinhaComConteudo: Int, linhaDoCursor: Int, folgaAbaixoDoCursor: Int, linhas: Int): Int {
        // AN ENTIRELY EMPTY GRID HAS NOTHING TO ANCHOR TO.
        //
        // Without this early exit, a grid without a single character fell back
        // on the cursor — which in an empty grid sits on row 0 — and the frame
        // dropped fifty rows, becoming a TWO-LINE STRIP glued to the footer,
        // with the application surface showing above it. That was exactly the
        // screen the owner photographed: "when the session opens everything is
        // black".
        //
        // Anchoring nothing is a question with no answer; the right answer is
        // to leave it alone and let whatever arrives fill the grid from the top
        // down, the way a terminal does.
        if (ultimaLinhaComConteudo < 0) return (linhas - 1).coerceAtLeast(0)
        val alvo = maxOf(ultimaLinhaComConteudo, linhaDoCursor + folgaAbaixoDoCursor)
        return alvo.coerceIn(0, (linhas - 1).coerceAtLeast(0))
    }

    /**
     * The last row with anything drawn on it, sweeping from the bottom up.
     *
     * `-1` when the whole grid is empty — and the caller treats that as
     * "nothing to anchor", not as "row zero".
     *
     * It sweeps from the bottom because that is where the answer usually is: on
     * a full screen it comes out on the first row examined. The worst case (an
     * empty grid) costs one pass over cells that are all spaces — a few thousand
     * comparisons, nowhere near mattering within a frame.
     *
     * @param vazia tells whether the cell `(x, y)` draws nothing. A space counts
     *   as empty ON PURPOSE: a frame whose footer ends with thirty columns of
     *   space must not pretend the row runs to the end.
     */
    inline fun ultimaLinhaComConteudo(colunas: Int, linhas: Int, vazia: (Int, Int) -> Boolean): Int {
        for (y in linhas - 1 downTo 0) {
            for (x in 0 until colunas) {
                if (!vazia(x, y)) return y
            }
        }
        return -1
    }
}
