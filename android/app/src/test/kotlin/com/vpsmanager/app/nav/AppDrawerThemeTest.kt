package com.vpsmanager.app.nav

import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.SemanticsNodeInteraction
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsSelected
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.text.TextLayoutResult
import com.vpsmanager.designsystem.THEME_SELECTOR_LABEL
import com.vpsmanager.designsystem.ThemeMode
import com.vpsmanager.designsystem.VpsManagerTheme
import com.vpsmanager.designsystem.themeOptionTag
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * The appearance choice inside the drawer: reachable, highlighted and with
 * no clipped label.
 *
 * The same phone geometry as [AppDrawerTest] and for the same reason:
 * three labels side by side within the drawer's width is exactly the kind
 * of thing that fits on the developer's emulator and gets clipped on the
 * device. The layout assertion below fails if any of them wraps over two
 * lines or overflows.
 */
@RunWith(RobolectricTestRunner::class)
@Config(application = android.app.Application::class, qualifiers = "w411dp-h891dp-xxhdpi")
class AppDrawerThemeTest {

    @get:Rule
    val composeRule = createComposeRule()

    private fun renderDrawer(
        themeMode: ThemeMode = ThemeMode.PADRAO,
        onThemeModeChange: (ThemeMode) -> Unit = {},
    ) {
        composeRule.setContent {
            VpsManagerTheme(themeMode = themeMode) {
                // The appearance choice MOVED HOUSE: it left the drawer's
                // footer and went to Settings, along with the update check. A
                // device setting is not a work destination, and mixed in with
                // System/Docker/Operations it made the drawer answer two
                // different questions. The test follows the selector to where
                // it went.
                TelaDeConfiguracoes(
                    themeMode = themeMode,
                    onThemeModeChange = onThemeModeChange,
                    versaoInstalada = "0.1.42",
                    onProcurarAtualizacao = {},
                    onAbrirNotificacoes = {},
                    onAbrirSeguranca = {},
                    onAbrirLicencas = {},
                    onAbrirDiagnostico = {},
                onAbrirArmazenamento = {},
                )
            }
        }
        composeRule.waitForIdle()
    }

    private fun SemanticsNodeInteraction.textLayout(): TextLayoutResult {
        val results = mutableListOf<TextLayoutResult>()
        val action = fetchSemanticsNode().config[SemanticsActions.GetTextLayoutResult]
        requireNotNull(action.action) { "nó sem GetTextLayoutResult — não é um texto?" }.invoke(results)
        return results.first()
    }

    @Test
    fun `a gaveta oferece as tres aparencias sem navegar para lugar nenhum`() {
        renderDrawer()

        composeRule.onNodeWithText(THEME_SELECTOR_LABEL).assertIsDisplayed()
        ThemeMode.entries.forEach { modo ->
            composeRule.onNodeWithTag(themeOptionTag(modo)).assertIsDisplayed()
        }
    }

    @Test
    fun `o modo corrente aparece marcado`() {
        renderDrawer(themeMode = ThemeMode.ESCURO)

        composeRule.onNodeWithTag(themeOptionTag(ThemeMode.ESCURO)).assertIsSelected()
    }

    @Test
    fun `tocar numa aparencia reporta a escolha`() {
        var escolhido: ThemeMode? = null
        renderDrawer(themeMode = ThemeMode.SISTEMA, onThemeModeChange = { escolhido = it })

        composeRule.onNodeWithTag(themeOptionTag(ThemeMode.CLARO)).performClick()

        assertEquals(ThemeMode.CLARO, escolhido)
    }

    @Test
    fun `nenhum rotulo do seletor quebra em duas linhas dentro da gaveta`() {
        renderDrawer()

        // The width itself is checked in ThemeModeSelectorWidthTest, in the
        // module that owns the component; what matters here is that the REAL
        // drawer — with its own breathing room and the other items competing
        // for space — makes no label wrap.
        ThemeMode.entries.forEach { modo ->
            val layout = composeRule.onNodeWithText(modo.label).textLayout()
            assertEquals("rótulo \"${modo.label}\" quebrou em mais de uma linha", 1, layout.lineCount)
        }
    }

    @Test
    fun `o seletor de aparencia abre a tela de Configuracoes, acima dos demais ajustes`() {
        renderDrawer()

        // Appearance is the first thing on the screen: it is the setting
        // touched most often, and the only one whose effect is visible at once.
        val seletor = composeRule.onNodeWithText(THEME_SELECTOR_LABEL)
            .fetchSemanticsNode().positionInRoot.y
        val atualizacao = composeRule.onNodeWithText(CHECK_UPDATE_LABEL)
            .fetchSemanticsNode().positionInRoot.y

        assertTrue("o seletor de aparência caiu para baixo dos outros ajustes", seletor < atualizacao)
    }
}
