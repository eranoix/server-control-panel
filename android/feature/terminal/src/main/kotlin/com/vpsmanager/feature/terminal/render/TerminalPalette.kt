package com.vpsmanager.feature.terminal.render

import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Immutable
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.luminance
import kotlin.math.abs

/**
 * The colours the terminal GRID uses — which are not, and cannot be, the
 * Material colours.
 *
 * ## Why the terminal has a palette of its own
 * Every cell in the grid carries the colour the REMOTE PROGRAM asked for (`ls
 * --color`, `git status`, an `htop`), already resolved to RGB by the VT
 * emulator. Material has no opinion about that, and should not: the app's
 * colour scheme decides buttons and cards, it does not decide what "red" means
 * in the output of a command. What the palette decides is everything else —
 * the grid's background, the colour of text with NO explicit colour, and the
 * cursor.
 *
 * ## What breaks in the light theme, and why [minLumaDelta] exists
 * The emulator's ANSI palette is built for a BLACK background. It returns pure
 * white for `\e[97m`, pure yellow for `\e[93m`, pure cyan for `\e[96m` —
 * colours that on a white background are not "low contrast", they are
 * INVISIBLE. That is the concrete case of an `ls --color` in any directory:
 * the names disappear. Switching the background to light without handling this
 * does not deliver a light theme, it delivers a blank terminal.
 *
 * [minLumaDelta] is the minimum luminance distance a text colour must keep
 * from the background it will land on. Anything too close is pushed away from
 * the background while preserving its hue (see [ajustaParaContraste]): yellow
 * stays yellow, only dark enough to read. The information's tone survives, and
 * so does legibility.
 *
 * In the DARK theme the value is 0 — the guard is off. That is not an
 * oversight: the dark renderer is the one shipping today, with pixel
 * conformance tests sitting on top of it, and a black background already gives
 * contrast to practically the whole ANSI palette. Turning the guard on there
 * would mean disturbing what works to solve a problem only the light theme
 * has.
 */
@Immutable
data class TerminalPalette(
    /** The colour of cell text with no colour of its own, 0xRRGGBB. */
    val defaultFg: Int,
    /** The background of the whole grid, 0xRRGGBB. */
    val defaultBg: Int,
    /** The colour of the cursor block. */
    val cursor: Color,
    /** Minimum luminance distance between text and background (0..255). 0 turns the guard off. */
    val minLumaDelta: Int,
)

/**
 * Exactly the colours the grid used before there was any theme choice — the
 * dark theme does not move a single pixel.
 */
val PaletaTerminalEscura = TerminalPalette(
    defaultFg = 0xE0E0E0,
    defaultBg = 0x000000,
    cursor = Color.White.copy(alpha = 0.4f),
    minLumaDelta = 0,
)

/**
 * A light terminal background, not paper white: 0xFAFAFA tires the eye less
 * than 0xFFFFFF on a phone screen and still leaves room for the selection
 * highlight to show. The default text is near-black rather than pure black for
 * the same reason.
 *
 * [minLumaDelta] = 190 is the lowest value at which ALL 16 ANSI colours, once
 * adjusted, clear WCAG's 4.5:1 contrast ratio against this background — the
 * worst case is pure red, which against white does not reach 4.5:1 even
 * unadjusted (3.99:1). It is not a number picked by eye: it came from
 * measuring the whole palette, and [TerminalPaletteTest] redoes the
 * measurement on every build.
 */
val PaletaTerminalClara = TerminalPalette(
    defaultFg = 0x1A1A1A,
    defaultBg = 0xFAFAFA,
    // A translucent BLACK cursor in the light theme. The translucent white it
    // used to be is literally invisible on a light background — the cursor
    // simply stops existing.
    cursor = Color.Black.copy(alpha = 0.35f),
    minLumaDelta = 190,
)

/**
 * The palette of the current theme.
 *
 * It derives light/dark from the colour scheme ITSELF rather than from
 * `isSystemInDarkTheme()`, for the same reason as
 * `com.vpsmanager.designsystem.vpsmStatusColors`: when the owner's manual
 * choice contradicts the device, the system gives the wrong answer — the
 * current scheme is the only source that already knows which theme is actually
 * painting.
 */
val paletaTerminalCorrente: TerminalPalette
    @Composable get() =
        if (MaterialTheme.colorScheme.surface.luminance() < 0.5f) PaletaTerminalEscura else PaletaTerminalClara

/**
 * The perceived luminance of a 0xRRGGBB colour, in 0..255 (ITU-R BT.601
 * coefficients).
 *
 * This is deliberately the CHEAP formula, not WCAG's sRGB linearisation: it
 * runs per CELL, per FRAME — an 80x40 grid at 60 Hz is 192 thousand calls a
 * second, and three `pow` calls apiece would show up in the frame budget. It
 * is the threshold in [PaletaTerminalClara] that was calibrated against the
 * real WCAG contrast ratio, in the test; here the only question is "far
 * enough?", and for that the approximation is plenty.
 */
internal fun luma(rgb: Int): Int {
    val r = (rgb shr 16) and 0xff
    val g = (rgb shr 8) and 0xff
    val b = rgb and 0xff
    return (299 * r + 587 * g + 114 * b) / 1000
}

/**
 * Returns [fg] pushed far enough from [bg] to be readable, preserving its hue;
 * returns [fg] untouched when the distance is already there, and always when
 * [minLumaDelta] is 0.
 *
 * ## How
 * The colour is blended towards BLACK (if the background is light) or WHITE
 * (if it is dark). Since [luma] is a linear combination of the channels, the
 * blend's luminance is linear in the fraction `t` — so the exact fraction that
 * hits the target falls out of a single division, with no search and no loop.
 * A pure white on a light background becomes dark grey; a pure yellow becomes
 * dark amber, still yellow.
 *
 * A pure function over integers: it is tested on the JVM, with no emulator
 * (see [TerminalPaletteTest]).
 */
internal fun ajustaParaContraste(fg: Int, bg: Int, minLumaDelta: Int): Int {
    if (minLumaDelta <= 0) return fg
    val lumaBg = luma(bg)
    val lumaFg = luma(fg)
    if (abs(lumaFg - lumaBg) >= minLumaDelta) return fg

    // A light background pulls the text towards black; a dark one, towards white.
    val fundoClaro = lumaBg >= 128
    val alvo = if (fundoClaro) 0 else 255

    // luma(blend) = lumaFg + t * (target - lumaFg). We want it to end up
    // minLumaDelta away from lumaBg, on the side opposite the background.
    val desejada = if (fundoClaro) lumaBg - minLumaDelta else lumaBg + minLumaDelta
    val denominador = alvo - lumaFg
    val t = if (denominador == 0) 1f else ((desejada - lumaFg).toFloat() / denominador).coerceIn(0f, 1f)

    return misturaCanais(fg, alvo, t)
}

/** Blends each channel of [rgb] towards [alvo] (0 or 255) by the fraction [t]. */
private fun misturaCanais(rgb: Int, alvo: Int, t: Float): Int {
    val r = canal((rgb shr 16) and 0xff, alvo, t)
    val g = canal((rgb shr 8) and 0xff, alvo, t)
    val b = canal(rgb and 0xff, alvo, t)
    return (r shl 16) or (g shl 8) or b
}

private fun canal(valor: Int, alvo: Int, t: Float): Int =
    (valor + (alvo - valor) * t).toInt().coerceIn(0, 255)
