package com.vpsmanager.app.nav

import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.navigation.NavHostController
import androidx.navigation.compose.rememberNavController
import com.vpsmanager.data.update.UpdateRecovery
import com.vpsmanager.data.update.UpdateState
import com.vpsmanager.designsystem.VpsManagerTheme
import java.net.URLEncoder
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * The update banner INSIDE the shell.
 *
 * What is proved here is not the banner's design (that is
 * `UpdateBannerTest`'s job), it is its PLACE: it lives in the
 * `Scaffold`'s `topBar` slot, next to the bar — the only point that
 * survives every drawer destination without being recomposed by
 * navigation, and where the `Scaffold` discounts its height from the
 * `innerPadding` on its own.
 *
 * And what it does NOT do: appear on the detail screens. There every dp
 * belongs to the content, and a banner on top of a call in progress is an
 * interruption, not a notice.
 */
@RunWith(RobolectricTestRunner::class)
@Config(application = android.app.Application::class, qualifiers = "w411dp-h891dp-xxhdpi")
class UpdateBannerInShellTest {

    @get:Rule
    val composeRule = createComposeRule()

    private lateinit var navController: NavHostController

    private fun renderShell(
        updateState: UpdateState,
        onUpdateClick: () -> Unit = {},
        onUpdateRecovery: (UpdateRecovery) -> Unit = {},
        updateDiagnostics: String? = null,
    ) {
        composeRule.setContent {
            VpsManagerTheme {
                navController = rememberNavController()
                AppNavHost(
                    updateState = updateState,
                    onUpdateClick = onUpdateClick,
                    onUpdateRecovery = onUpdateRecovery,
                    updateDiagnostics = updateDiagnostics,
                    navController = navController,
                )
            }
        }
        composeRule.waitForIdle()
    }

    @Test
    fun `a faixa aparece na casca, junto com o hamburguer, sem esconder o conteudo`() {
        renderShell(UpdateState.Available(versionName = "0.1.7", downloadBytes = 1_400_329, incremental = true))

        composeRule.onNodeWithText("Version 0.1.7 available — 1,4 MB").assertIsDisplayed()
        // The shell stays whole: the banner was added to the top, not put in
        // place of the bar.
        composeRule.onNodeWithContentDescription(OPEN_DRAWER_DESCRIPTION).assertIsDisplayed()
    }

    @Test
    fun `sem atualizacao nao ha faixa nenhuma ocupando altura`() {
        renderShell(UpdateState.Idle)

        composeRule.onNodeWithText("Update", substring = true).assertDoesNotExist()
    }

    /**
     * The same test that guarantees one header per screen:
     * `currentDestination == null` is exactly terminal, call and file editor.
     * The banner uses that SAME condition, so it cannot reappear there.
     */
    @Test
    fun `a faixa some nas telas de detalhe`() {
        renderShell(UpdateState.Available(versionName = "0.1.7", downloadBytes = 1_400_329, incremental = true))
        composeRule.onNodeWithText("Version 0.1.7 available — 1,4 MB").assertIsDisplayed()

        navController.navigate("arquivos/edit/" + URLEncoder.encode("/etc/hosts", "UTF-8"))
        composeRule.waitForIdle()

        composeRule.onNodeWithText("Version 0.1.7 available — 1,4 MB").assertDoesNotExist()
    }

    @Test
    fun `tocar na faixa pede a atualizacao`() {
        var requests = 0
        renderShell(
            UpdateState.Available(versionName = "0.1.7", downloadBytes = 1_400_329, incremental = true),
            onUpdateClick = { requests++ },
        )

        composeRule.onNodeWithText("Update").performClick()

        assertEquals(1, requests)
    }

    /**
     * A failed install: `PackageInstaller`'s message does not fit in a
     * banner, and the owner has no `adb`. The button leads to the Diagnostics
     * INSIDE the app — navigation, not a way out to the system, which is why
     * it is resolved here and does not go up as [UpdateRecovery] to
     * `MainActivity`.
     */
    @Test
    fun `o botao Diagnostico navega para o relatorio com a mensagem do sistema`() {
        var exitsToSystem = 0
        renderShell(
            UpdateState.Failed(
                "A instalação falhou: INSTALL_FAILED_UPDATE_INCOMPATIBLE",
                canRetry = true,
                recovery = UpdateRecovery.SHOW_DIAGNOSTICS,
            ),
            onUpdateRecovery = { exitsToSystem++ },
            updateDiagnostics = "Atualização — instalação falhou (status 4)\nINSTALL_FAILED_UPDATE_INCOMPATIBLE",
        )

        composeRule.onNodeWithText("Diagnostics").performClick()
        composeRule.waitForIdle()

        assertEquals("diagnostico", navController.currentBackStackEntry?.destination?.route)
        assertEquals("nada de sair do app para isto", 0, exitsToSystem)
        composeRule.onNodeWithText("INSTALL_FAILED_UPDATE_INCOMPATIBLE", substring = true).assertExists()
    }

    /** The ways out that ARE the system's (toggle, storage, browser) go up to whoever has an Activity. */
    @Test
    fun `as demais saidas sobem para a Activity em vez de virarem navegacao`() {
        var requested: UpdateRecovery? = null
        renderShell(
            UpdateState.Failed("sem permissão", canRetry = true, recovery = UpdateRecovery.ALLOW_UNKNOWN_SOURCES),
            onUpdateRecovery = { requested = it },
        )

        composeRule.onNodeWithText("Allow").performClick()

        assertEquals(UpdateRecovery.ALLOW_UNKNOWN_SOURCES, requested)
    }
}
