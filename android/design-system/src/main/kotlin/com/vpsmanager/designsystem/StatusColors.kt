package com.vpsmanager.designsystem

import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Immutable
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.luminance

/**
 * A state's colour pair: the card's background and the colour of the text/icon on it.
 */
@Immutable
data class StatusColorPair(
    val container: Color,
    val content: Color,
    val accent: Color,
)

/**
 * The app's three state colours, resolved against the current light/dark
 * theme.
 *
 * ## Why a colour exists that Material does not give
 * Material 3 has a semantic role for error (`error`/`errorContainer`) and none
 * for "warning". An operations panel needs both levels: if warning and error
 * share the same red, the operator loses the difference between "look today"
 * and "look now" — and starts treating both as the second, until they stop
 * treating either. The amber below is chosen to have enough contrast over its
 * own container in both themes.
 *
 * ## Why "ok" is not green
 * Bright green on a healthy state is the "sea of dots": nine green rows teach
 * the eye to skip the whole area, and on the day one of them turns red it
 * disappears with them. The healthy state here uses the theme's neutral
 * surface colour — present, discreet, not competing for attention. The green
 * [accent] is reserved for the ONE sentence confirming "nothing firing", never
 * for a list.
 */
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

/**
 * The state colours for the current theme. It is `@Composable` on purpose: it
 * reads `MaterialTheme.colorScheme`, so it automatically follows any theme
 * change without the screen having to know there was one.
 */
val vpsmStatusColors: StatusColors
    @Composable get() {
        val scheme = MaterialTheme.colorScheme
        // Derive light/dark from the scheme ITSELF, not from `isSystemInDarkTheme()`:
        // a screen that forces `VpsManagerTheme(darkTheme = …)` — a preview, a
        // test — would get the wrong answer from the system and would paint a
        // light amber over a dark background.
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
