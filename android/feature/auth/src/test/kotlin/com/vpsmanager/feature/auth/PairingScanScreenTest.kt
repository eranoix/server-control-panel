package com.vpsmanager.feature.auth

import android.Manifest
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.test.assertHasClickAction
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.test.core.app.ApplicationProvider
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.Shadows.shadowOf

/**
 * Renders [PairingScanScreen] under Robolectric.
 *
 * What these tests protect: **the screen degrades, it does not die.** The
 * previous code called `cameraProviderFuture.get()` inside a `Runnable` on the
 * main executor, with no `try`; on a device with no usable camera the future
 * fails, nothing catches the exception and the app CLOSES. Here every cause of
 * unavailability is injected through [AmbienteDaCamera] and what is verified is
 * that the screen renders a state explaining the cause and offering a way out.
 *
 * The injection is deliberate rather than relying on the emulator: the AVD's
 * camera configuration is shared state that another session turns on and off,
 * so a test tied to it proves nothing reliably. The HAPPY path (a real camera,
 * the CameraX pipeline, QR decoding) still requires a device/emulator, as
 * before.
 */
@RunWith(RobolectricTestRunner::class)
class PairingScanScreenTest {

    @get:Rule
    val composeRule = createComposeRule()

    private fun concederPermissaoDeCamera() {
        val context = ApplicationProvider.getApplicationContext<android.app.Application>()
        shadowOf(context).grantPermissions(Manifest.permission.CAMERA)
    }

    private fun ambiente(
        temPermissao: Boolean = true,
        podePedirNovamente: Boolean = true,
        vararg prontidoes: ProntidaoDaCamera,
    ): AmbienteDaCamera {
        var chamada = 0
        return AmbienteDaCamera(
            temPermissao = { temPermissao },
            podePedirPermissaoNovamente = { podePedirNovamente },
            checagem = { prontidoes[minOf(chamada++, prontidoes.size - 1)] },
        )
    }

    private fun renderizar(ambiente: AmbienteDaCamera, onManual: () -> Unit = {}, onLogin: () -> Unit = {}) {
        composeRule.setContent {
            PairingScanContent(
                modifier = Modifier,
                onPairingScanned = {},
                onManualSetupRequested = onManual,
                onLoginRequested = onLogin,
                ambiente = ambiente,
            )
        }
    }

    // --- the regression: with no camera, the screen renders instead of throwing ---

    @Test
    fun `sem nenhuma camera no aparelho a tela degrada, com a saida alternativa`() {
        renderizar(ambiente(prontidoes = arrayOf(ProntidaoDaCamera.Indisponivel(FalhaDeCamera.SemCamera))))

        composeRule.onNodeWithText("This device has no camera").assertExists()
        composeRule.onNodeWithText(ROTULO_CONFIGURAR_MANUALMENTE).assertHasClickAction()
        composeRule.onNodeWithText(ROTULO_JA_TENHO_ACESSO).assertHasClickAction()
    }

    /**
     * The test that fails on the old code and passes on the new one, through
     * the PUBLIC path and with no injection at all: under Robolectric the
     * `CameraManager` exposes no camera whatsoever — the same condition as the
     * lab emulator — and what is expected is the degraded screen, not a blank
     * preview (nor the process dying).
     */
    @Test
    fun `pelo caminho real, um aparelho sem camera cai no estado degradado`() {
        concederPermissaoDeCamera()

        composeRule.setContent { PairingScanScreen(onPairingScanned = {}, onManualSetupRequested = {}) }

        composeRule.onNodeWithText("This device has no camera").assertExists()
        composeRule.onNodeWithText(ROTULO_CONFIGURAR_MANUALMENTE).assertExists()
    }

    @Test
    fun `renderiza sem quebrar antes de a permissao ser concedida`() {
        composeRule.setContent { PairingScanScreen(onPairingScanned = {}) }

        composeRule.onRoot().assertExists()
    }

    @Test
    fun `enquanto a permissao e pedida as saidas continuam de pe - spinner sozinho tambem e beco`() {
        renderizar(
            ambiente(
                temPermissao = false,
                prontidoes = arrayOf(ProntidaoDaCamera.Pronta(usarCameraFrontal = false)),
            ),
        )

        composeRule.onNodeWithText(ROTULO_CONFIGURAR_MANUALMENTE).assertHasClickAction()
        composeRule.onNodeWithText(ROTULO_JA_TENHO_ACESSO).assertHasClickAction()
    }

    // --- each distinct cause, with the action that matches it ---

    @Test
    fun `camera ocupada por outro app oferece tentar de novo, e a nova tentativa reconfere`() {
        renderizar(
            ambiente(
                prontidoes = arrayOf(
                    ProntidaoDaCamera.Indisponivel(FalhaDeCamera.CameraOcupada),
                    ProntidaoDaCamera.Indisponivel(FalhaDeCamera.SemCamera),
                ),
            ),
        )

        composeRule.onNodeWithText("The camera is in use").assertExists()
        composeRule.onNodeWithText("Try again").performClick()
        composeRule.waitForIdle()

        // The second check returned a different cause: proof that the button
        // really did re-check instead of merely repainting the same screen.
        composeRule.onNodeWithText("This device has no camera").assertExists()
    }

    @Test
    fun `permissao negada pede de novo, e nao manda para as configuracoes`() {
        var pedidos = 0
        composeRule.setContent {
            CameraIndisponivelContent(
                falha = FalhaDeCamera.PermissaoNegada,
                onPedirPermissao = { pedidos++ },
                onAbrirConfiguracoes = { throw AssertionError("não deve abrir Configurações aqui") },
                onTentarNovamente = { throw AssertionError("não deve reconferir sem permissão") },
                onManualSetupRequested = {},
                onLoginRequested = {},
            )
        }

        composeRule.onNodeWithText("No camera access").assertExists()
        composeRule.onNodeWithText("Allow camera access").performClick()

        assertEquals(1, pedidos)
    }

    @Test
    fun `permissao negada em definitivo leva as configuracoes, sem insistir no dialogo`() {
        var configuracoes = 0
        composeRule.setContent {
            CameraIndisponivelContent(
                falha = FalhaDeCamera.PermissaoBloqueada,
                onPedirPermissao = { throw AssertionError("pedir de novo aqui é o laço que queremos evitar") },
                onAbrirConfiguracoes = { configuracoes++ },
                onTentarNovamente = {},
                onManualSetupRequested = {},
                onLoginRequested = {},
            )
        }

        composeRule.onNodeWithText("Camera access blocked").assertExists()
        composeRule.onNodeWithText("Open app settings").performClick()

        assertEquals(1, configuracoes)
    }

    @Test
    fun `sem camera nao oferece retentar - so os caminhos alternativos`() {
        var manual = 0
        var login = 0
        composeRule.setContent {
            CameraIndisponivelContent(
                falha = FalhaDeCamera.SemCamera,
                onPedirPermissao = {},
                onAbrirConfiguracoes = {},
                onTentarNovamente = { throw AssertionError("não há o que retentar sem câmera") },
                onManualSetupRequested = { manual++ },
                onLoginRequested = { login++ },
            )
        }

        composeRule.onNodeWithText("Try again").assertDoesNotExist()
        composeRule.onNodeWithText(ROTULO_CONFIGURAR_MANUALMENTE).performClick()
        composeRule.onNodeWithText(ROTULO_JA_TENHO_ACESSO).performClick()

        assertEquals(1, manual)
        assertEquals(1, login)
    }

    @Test
    fun `falha inesperada e honesta - mostra o detalhe tecnico para o dono relatar`() {
        renderizar(
            ambiente(
                prontidoes = arrayOf(
                    ProntidaoDaCamera.Indisponivel(
                        FalhaDeCamera.FalhaInesperada("IllegalStateException: provedor não subiu"),
                    ),
                ),
            ),
        )

        composeRule.onNodeWithText("Could not open the camera").assertExists()
        composeRule.onNodeWithText("IllegalStateException: provedor não subiu").assertExists()
        composeRule.onNodeWithText("Try again").assertHasClickAction()
    }

    @Test
    fun `camera bloqueada pelo sistema tem texto proprio, distinto de ocupada`() {
        renderizar(
            ambiente(
                prontidoes = arrayOf(
                    ProntidaoDaCamera.Indisponivel(FalhaDeCamera.CameraBloqueadaPeloSistema),
                ),
            ),
        )

        composeRule.onNodeWithText("The camera is disabled by the system").assertExists()
        composeRule.onNodeWithText("The camera is in use").assertDoesNotExist()
    }

    @Test
    fun `toda tela degradada mantem as duas saidas do pareamento por QR`() {
        val causas = listOf(
            FalhaDeCamera.PermissaoNegada,
            FalhaDeCamera.PermissaoBloqueada,
            FalhaDeCamera.SemCamera,
            FalhaDeCamera.CameraOcupada,
            FalhaDeCamera.CameraBloqueadaPeloSistema,
            FalhaDeCamera.FalhaInesperada("x"),
        )
        var causaAtual by mutableStateOf<FalhaDeCamera>(FalhaDeCamera.PermissaoNegada)

        composeRule.setContent {
            CameraIndisponivelContent(
                falha = causaAtual,
                onPedirPermissao = {},
                onAbrirConfiguracoes = {},
                onTentarNovamente = {},
                onManualSetupRequested = {},
                onLoginRequested = {},
            )
        }

        causas.forEach { causa ->
            causaAtual = causa
            composeRule.waitForIdle()
            composeRule.onNodeWithText(ROTULO_CONFIGURAR_MANUALMENTE).assertHasClickAction()
            composeRule.onNodeWithText(ROTULO_JA_TENHO_ACESSO).assertHasClickAction()
        }
        assertTrue("nenhuma causa pode ficar sem saída", causas.isNotEmpty())
    }
}
