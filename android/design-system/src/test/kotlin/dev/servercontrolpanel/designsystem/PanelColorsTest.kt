package dev.servercontrolpanel.designsystem

import androidx.compose.material3.ColorScheme
import androidx.compose.ui.graphics.Color
import org.junit.Assert.assertTrue
import org.junit.Test
import kotlin.math.pow

class PanelColorsTest {

    private val minimumAA = 4.5

    private fun linearChannel(v: Float): Double {
        val s = v.toDouble()
        return if (s <= 0.03928) s / 12.92 else ((s + 0.055) / 1.055).pow(2.4)
    }

    private fun luminance(c: Color): Double =
        0.2126 * linearChannel(c.red) + 0.7152 * linearChannel(c.green) + 0.0722 * linearChannel(c.blue)

    private fun contrast(a: Color, b: Color): Double {
        val la = luminance(a)
        val lb = luminance(b)
        return (maxOf(la, lb) + 0.05) / (minOf(la, lb) + 0.05)
    }

    private fun pairs(e: ColorScheme): List<Triple<String, Color, Color>> = listOf(
        Triple("primary/onPrimary", e.primary, e.onPrimary),
        Triple("primaryContainer/on", e.primaryContainer, e.onPrimaryContainer),
        Triple("secondary/onSecondary", e.secondary, e.onSecondary),
        Triple("secondaryContainer/on", e.secondaryContainer, e.onSecondaryContainer),
        Triple("tertiary/onTertiary", e.tertiary, e.onTertiary),
        Triple("tertiaryContainer/on", e.tertiaryContainer, e.onTertiaryContainer),
        Triple("error/onError", e.error, e.onError),
        Triple("errorContainer/on", e.errorContainer, e.onErrorContainer),
        Triple("background/onBackground", e.background, e.onBackground),
        Triple("surface/onSurface", e.surface, e.onSurface),
        Triple("surfaceVariant/onSurfaceVariant", e.surfaceVariant, e.onSurfaceVariant),
        Triple("surfaceContainer/onSurface", e.surfaceContainer, e.onSurface),
        Triple("surfaceContainerHigh/onSurface", e.surfaceContainerHigh, e.onSurface),
        Triple("surfaceContainerHigh/onSurfaceVariant", e.surfaceContainerHigh, e.onSurfaceVariant),
        Triple("surfaceContainerHighest/onSurface", e.surfaceContainerHighest, e.onSurface),
        Triple("inverseSurface/inverseOnSurface", e.inverseSurface, e.inverseOnSurface),
    )

    private fun checkScheme(name: String, scheme: ColorScheme) {
        pairs(scheme).forEach { (role, background, foreground) ->
            val ratio = contrast(background, foreground)
            assertTrue(
                "[$name] $role is ${"%.2f".format(ratio)}:1, below the AA minimum of $minimumAA:1",
                ratio >= minimumAA,
            )
        }
    }

    @Test
    fun `every color pair passes AA in the light theme`() {
        checkScheme("light", PanelLightColors)
    }

    @Test
    fun `every color pair passes AA in the dark theme`() {
        checkScheme("dark", PanelDarkColors)
    }

    @Test
    fun `the app no longer uses Material's default purple`() {
        val baselinePurpleLight = Color(0xFF6750A4)
        val baselinePurpleDark = Color(0xFFD0BCFF)
        assertTrue("light theme fell back to the baseline purple", PanelLightColors.primary != baselinePurpleLight)
        assertTrue("dark theme fell back to the baseline purple", PanelDarkColors.primary != baselinePurpleDark)
    }

    @Test
    fun `the two themes really are light and dark`() {
        assertTrue("the light surface should be light", luminance(PanelLightColors.surface) > 0.5)
        assertTrue("the dark surface should be dark", luminance(PanelDarkColors.surface) < 0.1)
    }

    @Test
    fun `the warning amber stays visible on the surfaces`() {
        val warningLightBackground = Color(0xFFFFEBB8)
        val warningLightText = Color(0xFF4A3400)
        val warningDarkBackground = Color(0xFF3E2D00)
        val warningDarkText = Color(0xFFFFE2A6)

        assertTrue(
            "light warning text is illegible on its card",
            contrast(warningLightBackground, warningLightText) >= minimumAA,
        )
        assertTrue(
            "dark warning text is illegible on its card",
            contrast(warningDarkBackground, warningDarkText) >= minimumAA,
        )
        assertTrue(
            "light warning card blends into the surface",
            contrast(warningLightBackground, PanelLightColors.surface) >= 1.08,
        )
        assertTrue(
            "dark warning card blends into the surface",
            contrast(warningDarkBackground, PanelDarkColors.surface) >= 1.08,
        )
    }

    @Test
    fun `the adaptive icon colors come from the scheme`() {
        assertTrue(AdaptiveIconBackground == PanelDarkColors.onPrimary)
        assertTrue(MarkOnBackground == PanelDarkColors.primary)
        assertTrue(
            "the mark must be legible on the icon background",
            contrast(AdaptiveIconBackground, MarkOnBackground) >= minimumAA,
        )
    }
}
