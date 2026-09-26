package com.vpsmanager.designsystem

import androidx.compose.material3.ColorScheme
import androidx.compose.ui.graphics.Color
import org.junit.Assert.assertTrue
import org.junit.Test
import kotlin.math.pow

/**
 * The brand's colour scheme stays LEGIBLE.
 *
 * A palette is the easiest thing to break without noticing: someone nudges a
 * hex because "it looked nicer" and a button's text falls to 3:1, which on a
 * phone screen in the sun — which is where this app is used — stops being
 * readable. This test is the ruler: it measures the WCAG contrast ratio of
 * every (colour, on-colour) pair Material 3 promises is legible, in BOTH
 * themes, and fails if any of them drops below AA.
 *
 * It is a pure JVM test, no Robolectric: `Color` is a `value class` over a
 * `ULong` and the channels come out of it without touching the framework.
 */
class VpsmColorsTest {

    /** WCAG AA minimum for normal text. */
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

    /**
     * The pairs Material 3 promises are legible. An `onX` role exists exactly
     * to be drawn over `X` — if the pair does not pass, the promise is false
     * and some screen of the app has text that disappears.
     */
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
                "[$name] $role ficou em ${"%.2f".format(ratio)}:1 — abaixo do mínimo AA de $minimumAA:1",
                ratio >= minimumAA,
            )
        }
    }

    @Test
    fun `todo par cor sobre-cor passa em AA no tema claro`() {
        checkScheme("claro", VpsmLightColors)
    }

    @Test
    fun `todo par cor sobre-cor passa em AA no tema escuro`() {
        checkScheme("escuro", VpsmDarkColors)
    }

    @Test
    fun `o app nao esta mais vestindo o roxo de fabrica do Material`() {
        // The regression this test guards against is literal: a
        // `lightColorScheme()` with no arguments returns the baseline purple
        // (#6750A4 / #D0BCFF) and the app goes back to looking like a demo.
        val baselinePurpleLight = Color(0xFF6750A4)
        val baselinePurpleDark = Color(0xFFD0BCFF)
        assertTrue("tema claro voltou ao roxo baseline", VpsmLightColors.primary != baselinePurpleLight)
        assertTrue("tema escuro voltou ao roxo baseline", VpsmDarkColors.primary != baselinePurpleDark)
    }

    @Test
    fun `os dois temas sao mesmo claro e escuro`() {
        // Deliberately silly guard: a copy-paste that left both surfaces on
        // the same side of the scale would pass every contrast test above and
        // still produce a white "dark theme".
        assertTrue("a superfície clara deveria ser clara", luminance(VpsmLightColors.surface) > 0.5)
        assertTrue("a superfície escura deveria ser escura", luminance(VpsmDarkColors.surface) < 0.1)
    }

    @Test
    fun `o ambar de atencao continua visivel sobre as superficies novas`() {
        // The new palette must NOT wash the warning out — this is the case the
        // brief calls "a pretty palette worse than the factory purple". The
        // amber does not derive from the scheme (Material has no role for
        // "warning"), so it is precisely the one a swap of neutrals could
        // leave without contrast against the surface the card is drawn on.
        val warningLightBackground = Color(0xFFFFEBB8)
        val warningLightText = Color(0xFF4A3400)
        val warningDarkBackground = Color(0xFF3E2D00)
        val warningDarkText = Color(0xFFFFE2A6)

        assertTrue(
            "texto do aviso claro ilegível no próprio cartão",
            contrast(warningLightBackground, warningLightText) >= minimumAA,
        )
        assertTrue(
            "texto do aviso escuro ilegível no próprio cartão",
            contrast(warningDarkBackground, warningDarkText) >= minimumAA,
        )
        // And the card has to STAND OUT from the surface: an amber that became
        // the same colour as the background would warn about nothing.
        assertTrue(
            "o cartão de aviso claro sumiu na superfície",
            contrast(warningLightBackground, VpsmLightColors.surface) >= 1.08,
        )
        assertTrue(
            "o cartão de aviso escuro sumiu na superfície",
            contrast(warningDarkBackground, VpsmDarkColors.surface) >= 1.08,
        )
    }

    @Test
    fun `a cor do icone adaptativo e a mesma do esquema, nao uma cor solta`() {
        // Another agent builds the icon's `res/` from these constants. If
        // someone changes the scheme and forgets the icon, the icon stops
        // belonging to the same family — this test ties the two together.
        assertTrue(AdaptiveIconBackground == VpsmDarkColors.onPrimary)
        assertTrue(MarkOnBackground == VpsmDarkColors.primary)
        assertTrue(
            "a marca precisa ser legível sobre o fundo do ícone",
            contrast(AdaptiveIconBackground, MarkOnBackground) >= minimumAA,
        )
    }
}
