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

    private fun sistemaEscuro(escuro: Boolean) {
        RuntimeEnvironment.setQualifiers(if (escuro) "+night" else "+notnight")
    }

    /** Luminance of the surface the theme applied — the proof of which scheme went in. */
    private fun superficieAplicada(themeMode: ThemeMode): Float {
        var superficie = Color.Unspecified
        composeRule.setContent {
            VpsManagerTheme(themeMode = themeMode) {
                superficie = MaterialTheme.colorScheme.surface
                Text("x")
            }
        }
        composeRule.waitForIdle()
        return superficie.luminance()
    }

    @Test
    fun `seguir o sistema pinta escuro quando o sistema esta escuro`() {
        sistemaEscuro(true)
        assertTrue(superficieAplicada(ThemeMode.SISTEMA) < 0.5f)
    }

    @Test
    fun `seguir o sistema pinta claro quando o sistema esta claro`() {
        sistemaEscuro(false)
        assertTrue(superficieAplicada(ThemeMode.SISTEMA) >= 0.5f)
    }

    @Test
    fun `escolha CLARO ignora o sistema no escuro`() {
        sistemaEscuro(true)
        // The whole point of the feature: the device is in night mode and the
        // app paints light anyway.
        assertTrue(superficieAplicada(ThemeMode.CLARO) >= 0.5f)
    }

    @Test
    fun `escolha ESCURO ignora o sistema no claro`() {
        sistemaEscuro(false)
        assertTrue(superficieAplicada(ThemeMode.ESCURO) < 0.5f)
    }

    @Test
    fun `as cores de estado seguem a escolha manual, nao o sistema`() {
        // StatusColors derives light/dark from the scheme ITSELF (StatusColors.kt).
        // This test is the proof that that stays right when the choice
        // contradicts the device: system DARK + choice LIGHT has to give the
        // LIGHT amber (pale background, dark text) — the opposite would be a
        // dark amber over a light surface.
        sistemaEscuro(true)
        var aviso = StatusColorPair(Color.Unspecified, Color.Unspecified, Color.Unspecified)
        composeRule.setContent {
            VpsManagerTheme(themeMode = ThemeMode.CLARO) {
                aviso = vpsmStatusColors.warning
                Text("x")
            }
        }
        composeRule.waitForIdle()

        assertTrue("container de aviso deveria ser claro", aviso.container.luminance() > 0.5f)
        assertTrue("texto de aviso deveria ser escuro", aviso.content.luminance() < 0.5f)
    }

    @Test
    fun `trocar a escolha repinta sem recriar a tela`() {
        sistemaEscuro(false)
        var modo by mutableStateOf(ThemeMode.CLARO)
        var superficie = Color.Unspecified
        composeRule.setContent {
            VpsManagerTheme(themeMode = modo) {
                superficie = MaterialTheme.colorScheme.surface
                Text("x")
            }
        }
        composeRule.waitForIdle()
        assertTrue(superficie.luminance() >= 0.5f)

        modo = ThemeMode.ESCURO
        composeRule.waitForIdle()
        assertTrue("a troca tem que valer na recomposição", superficie.luminance() < 0.5f)
    }

    @Test
    fun `o seletor marca o modo corrente e reporta o escolhido`() {
        var escolhido: ThemeMode? = null
        composeRule.setContent {
            VpsManagerTheme(themeMode = ThemeMode.SISTEMA) {
                ThemeModeSelector(selected = ThemeMode.SISTEMA, onSelect = { escolhido = it })
            }
        }

        composeRule.onNodeWithTag(themeOptionTag(ThemeMode.SISTEMA)).assertIsSelected()
        composeRule.onNodeWithTag(themeOptionTag(ThemeMode.CLARO)).performClick()

        assertEquals(ThemeMode.CLARO, escolhido)
    }

    @Test
    fun `o seletor oferece as tres opcoes`() {
        composeRule.setContent {
            VpsManagerTheme {
                ThemeModeSelector(selected = ThemeMode.SISTEMA, onSelect = {})
            }
        }

        // No typing, no menu: the three alternatives are on the screen.
        ThemeMode.entries.forEach { modo ->
            composeRule.onNodeWithTag(themeOptionTag(modo)).assertExists()
        }
    }
}
