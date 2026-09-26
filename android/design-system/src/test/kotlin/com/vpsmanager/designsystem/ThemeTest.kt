package com.vpsmanager.designsystem

import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.luminance
import androidx.compose.ui.test.assertIsSelected
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.RuntimeEnvironment

/**
 * What the appearance choice has to do to the real COLOURS.
 *
 * Robolectric's `night` qualifier is what `isSystemInDarkTheme()` reads, so
 * flipping it is the only honest way to prove "follow the system reacts" and
 * "a manual choice ignores the system" on the JVM — asserting on the boolean
 * alone would not prove the theme actually painted with the right scheme.
 */
@RunWith(RobolectricTestRunner::class)
class ThemeTest {

    @get:Rule
    val composeRule = createComposeRule()

    private fun systemDark(dark: Boolean) {
        RuntimeEnvironment.setQualifiers(if (dark) "+night" else "+notnight")
    }

    /** Luminance of the surface the theme applied — the proof of which scheme went in. */
    private fun appliedSurface(themeMode: ThemeMode): Float {
        var surface = Color.Unspecified
        composeRule.setContent {
            VpsManagerTheme(themeMode = themeMode) {
                surface = MaterialTheme.colorScheme.surface
                Text("x")
            }
        }
        composeRule.waitForIdle()
        return surface.luminance()
    }

    @Test
    fun `seguir o sistema pinta escuro quando o sistema esta escuro`() {
        systemDark(true)
        assertTrue(appliedSurface(ThemeMode.SYSTEM) < 0.5f)
    }

    @Test
    fun `seguir o sistema pinta claro quando o sistema esta claro`() {
        systemDark(false)
        assertTrue(appliedSurface(ThemeMode.SYSTEM) >= 0.5f)
    }

    @Test
    fun `escolha CLARO ignora o sistema no escuro`() {
        systemDark(true)
        // The whole point of the feature: the device is in night mode and the
        // app paints light anyway.
        assertTrue(appliedSurface(ThemeMode.LIGHT) >= 0.5f)
    }

    @Test
    fun `escolha ESCURO ignora o sistema no claro`() {
        systemDark(false)
        assertTrue(appliedSurface(ThemeMode.DARK) < 0.5f)
    }

    @Test
    fun `as cores de estado seguem a escolha manual, nao o sistema`() {
        // StatusColors derives light/dark from the scheme ITSELF (StatusColors.kt).
        // This test is the proof that that stays right when the choice
        // contradicts the device: system DARK + choice LIGHT has to give the
        // LIGHT amber (pale background, dark text) — the opposite would be a
        // dark amber over a light surface.
        systemDark(true)
        var notification = StatusColorPair(Color.Unspecified, Color.Unspecified, Color.Unspecified)
        composeRule.setContent {
            VpsManagerTheme(themeMode = ThemeMode.LIGHT) {
                notification = vpsmStatusColors.warning
                Text("x")
            }
        }
        composeRule.waitForIdle()

        assertTrue("container de aviso deveria ser claro", notification.container.luminance() > 0.5f)
        assertTrue("texto de aviso deveria ser escuro", notification.content.luminance() < 0.5f)
    }

    @Test
    fun `trocar a escolha repinta sem recriar a tela`() {
        systemDark(false)
        var mode by mutableStateOf(ThemeMode.LIGHT)
        var surface = Color.Unspecified
        composeRule.setContent {
            VpsManagerTheme(themeMode = mode) {
                surface = MaterialTheme.colorScheme.surface
                Text("x")
            }
        }
        composeRule.waitForIdle()
        assertTrue(surface.luminance() >= 0.5f)

        mode = ThemeMode.DARK
        composeRule.waitForIdle()
        assertTrue("a troca tem que valer na recomposição", surface.luminance() < 0.5f)
    }

    @Test
    fun `o seletor marca o modo corrente e reporta o escolhido`() {
        var chosen: ThemeMode? = null
        composeRule.setContent {
            VpsManagerTheme(themeMode = ThemeMode.SYSTEM) {
                ThemeModeSelector(selected = ThemeMode.SYSTEM, onSelect = { chosen = it })
            }
        }

        composeRule.onNodeWithTag(themeOptionTag(ThemeMode.SYSTEM)).assertIsSelected()
        composeRule.onNodeWithTag(themeOptionTag(ThemeMode.LIGHT)).performClick()

        assertEquals(ThemeMode.LIGHT, chosen)
    }

    @Test
    fun `o seletor oferece as tres opcoes`() {
        composeRule.setContent {
            VpsManagerTheme {
                ThemeModeSelector(selected = ThemeMode.SYSTEM, onSelect = {})
            }
        }

        // No typing, no menu: the three alternatives are on the screen.
        ThemeMode.entries.forEach { mode ->
            composeRule.onNodeWithTag(themeOptionTag(mode)).assertExists()
        }
    }
}
