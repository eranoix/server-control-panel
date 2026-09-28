package dev.servercontrolpanel.feature.terminal.render

import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Immutable
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.luminance
import kotlin.math.abs

@Immutable
data class TerminalPalette(
    val defaultFg: Int,
    val defaultBg: Int,
    val cursor: Color,
    val minLumaDelta: Int,
)

val DarkTerminalPalette = TerminalPalette(
    defaultFg = 0xE0E0E0,
    defaultBg = 0x000000,
    cursor = Color.White.copy(alpha = 0.4f),
    minLumaDelta = 0,
)

val LightTerminalPalette = TerminalPalette(
    defaultFg = 0x1A1A1A,
    defaultBg = 0xFAFAFA,
    cursor = Color.Black.copy(alpha = 0.35f),
    minLumaDelta = 190,
)

val currentTerminalPalette: TerminalPalette
    @Composable get() =
        if (MaterialTheme.colorScheme.surface.luminance() < 0.5f) DarkTerminalPalette else LightTerminalPalette

internal fun luma(rgb: Int): Int {
    val r = (rgb shr 16) and 0xff
    val g = (rgb shr 8) and 0xff
    val b = rgb and 0xff
    return (299 * r + 587 * g + 114 * b) / 1000
}

internal fun adjustForContrast(fg: Int, bg: Int, minLumaDelta: Int): Int {
    if (minLumaDelta <= 0) return fg
    val lumaBg = luma(bg)
    val lumaFg = luma(fg)
    if (abs(lumaFg - lumaBg) >= minLumaDelta) return fg

    val lightBackground = lumaBg >= 128
    val target = if (lightBackground) 0 else 255

    val desired = if (lightBackground) lumaBg - minLumaDelta else lumaBg + minLumaDelta
    val denominator = target - lumaFg
    val t = if (denominator == 0) 1f else ((desired - lumaFg).toFloat() / denominator).coerceIn(0f, 1f)

    return mixChannels(fg, target, t)
}

private fun mixChannels(rgb: Int, target: Int, t: Float): Int {
    val r = channel((rgb shr 16) and 0xff, target, t)
    val g = channel((rgb shr 8) and 0xff, target, t)
    val b = channel(rgb and 0xff, target, t)
    return (r shl 16) or (g shl 8) or b
}

private fun channel(value: Int, target: Int, t: Float): Int =
    (value + (target - value) * t).toInt().coerceIn(0, 255)
