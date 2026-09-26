package com.vpsmanager.designsystem

import android.app.Activity
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.SideEffect
import androidx.compose.ui.platform.LocalInspectionMode
import androidx.compose.ui.platform.LocalView
import androidx.core.view.WindowCompat

/**
 * The app's single Material3 theme entry point.
 *
 * [themeMode] is the owner's choice ([ThemePreference]); the default is still
 * "follow the system", which is what this theme did before there was any
 * choice at all. [darkTheme] stays as a derived parameter so a preview or a
 * test can force either side without inventing a preference — the same role
 * it always had.
 *
 * Nobody below this point asks the system whether it is dark: whoever needs
 * to know derives it from the colour scheme ITSELF (see [vpsmStatusColors]),
 * which keeps the answer right even when the manual choice contradicts the
 * device.
 *
 * The colours are those of [VpsmLightColors]/[VpsmDarkColors] — the scheme
 * derived from the control panel's identity. This file once called
 * `lightColorScheme()` and `darkColorScheme()` WITH NO ARGUMENTS, which
 * dressed the whole app in Material's baseline purple and was the direct
 * cause of it looking like an unstyled demo.
 */
@Composable
fun VpsManagerTheme(
    themeMode: ThemeMode = ThemeMode.PADRAO,
    darkTheme: Boolean = themeMode.escuro(isSystemInDarkTheme()),
    content: @Composable () -> Unit,
) {
    val colorScheme = if (darkTheme) VpsmDarkColors else VpsmLightColors
    AjustaBarrasDoSistema(darkTheme)
    MaterialTheme(
        colorScheme = colorScheme,
        content = content,
    )
}

/**
 * Puts the status bar's and navigation bar's icons at the right contrast for
 * [escuro].
 *
 * The app draws edge to edge (`enableEdgeToEdge()`), so those two bars are
 * TRANSPARENT and show the screen's background underneath. What decides
 * whether the clock, battery and navigation button icons are black or white
 * is this pair of flags — and `enableEdgeToEdge()` resolves them ONCE, in
 * `onCreate`, looking at the SYSTEM's mode. With a manual choice that becomes
 * wrong at exactly the moment the choice contradicts the device: a light
 * theme forced on a phone in night mode ended up with a white clock on a
 * white background — the bar vanishes.
 *
 * It runs in a `SideEffect` because it mutates window state (outside the
 * Compose world) and has to happen after the composition takes, not during
 * it. With no Activity (a `@Preview`, a test that hosts no window) there is
 * no bar to adjust and the function is a no-op.
 */
@Composable
private fun AjustaBarrasDoSistema(escuro: Boolean) {
    val view = LocalView.current
    if (LocalInspectionMode.current || view.isInEditMode) return
    SideEffect {
        val window = (view.context as? Activity)?.window ?: return@SideEffect
        val controller = WindowCompat.getInsetsController(window, view)
        // "LIGHT bar appearance" = DARK icons, for a light background.
        controller.isAppearanceLightStatusBars = !escuro
        controller.isAppearanceLightNavigationBars = !escuro
    }
}
