package dev.servercontrolpanel.designsystem

import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Immutable
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.luminance

@Immutable
data class StatusColorPair(
    val container: Color,
    val content: Color,
    val accent: Color,
)

@Immutable
data class StatusColors(
    val ok: StatusColorPair,
    val warning: StatusColorPair,
    val critical: StatusColorPair,
)

private val WarningLight = StatusColorPair(
    container = Color(0xFFFFEBB8),
    content = Color(0xFF4A3400),
    accent = Color(0xFF8A6000),
)

private val WarningDark = StatusColorPair(
    container = Color(0xFF3E2D00),
    content = Color(0xFFFFE2A6),
    accent = Color(0xFFFFC248),
)

private val OkAccentLight = Color(0xFF2E6B3E)
private val OkAccentDark = Color(0xFF8FD9A3)

val panelStatusColors: StatusColors
    @Composable get() {
        val scheme = MaterialTheme.colorScheme
        val dark = scheme.surface.luminance() < 0.5f
        return StatusColors(
            ok = StatusColorPair(
                container = scheme.surfaceContainerHigh,
                content = scheme.onSurfaceVariant,
                accent = if (dark) OkAccentDark else OkAccentLight,
            ),
            warning = if (dark) WarningDark else WarningLight,
            critical = StatusColorPair(
                container = scheme.errorContainer,
                content = scheme.onErrorContainer,
                accent = scheme.error,
            ),
        )
    }
