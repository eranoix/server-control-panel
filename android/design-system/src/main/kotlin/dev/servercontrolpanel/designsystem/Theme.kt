package dev.servercontrolpanel.designsystem

import android.app.Activity
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.SideEffect
import androidx.compose.ui.platform.LocalInspectionMode
import androidx.compose.ui.platform.LocalView
import androidx.core.view.WindowCompat

@Composable
fun PanelTheme(
    themeMode: ThemeMode = ThemeMode.DEFAULT,
    darkTheme: Boolean = themeMode.dark(isSystemInDarkTheme()),
    content: @Composable () -> Unit,
) {
    val colorScheme = if (darkTheme) PanelDarkColors else PanelLightColors
    AdjustSystemBars(darkTheme)
    MaterialTheme(
        colorScheme = colorScheme,
        content = content,
    )
}

@Composable
private fun AdjustSystemBars(dark: Boolean) {
    val view = LocalView.current
    if (LocalInspectionMode.current || view.isInEditMode) return
    SideEffect {
        val window = (view.context as? Activity)?.window ?: return@SideEffect
        val controller = WindowCompat.getInsetsController(window, view)
        controller.isAppearanceLightStatusBars = !dark
        controller.isAppearanceLightNavigationBars = !dark
    }
}
