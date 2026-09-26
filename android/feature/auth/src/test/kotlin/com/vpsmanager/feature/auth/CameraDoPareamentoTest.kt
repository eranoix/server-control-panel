package com.vpsmanager.feature.auth

import androidx.camera.core.CameraState
import androidx.camera.core.CameraUnavailableException
import androidx.camera.core.InitializationException
import java.io.IOException
import java.util.concurrent.ExecutionException
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Classifying a camera failure is deliberately pure — no Compose, no
 * Robolectric and no hardware — precisely because it is the piece that has to
 * be right cause by cause: the action the screen offers (ask for permission,
 * open Settings, try again, give up on the camera) comes from here.
 *
 * The exceptions assembled here reproduce CameraX's REAL wrapping: the
 * `ListenableFuture` fails with an `ExecutionException` wrapping an
 * `InitializationException`, which in turn wraps the
 * `CameraUnavailableException` carrying the reason.
 */
class CameraDoPareamentoTest {

    private fun comoOCameraXEntrega(causa: Throwable): Throwable =
        ExecutionException(InitializationException(causa))

    @Test
    fun `emulador sem camera - a falha real que fechava o app vira SemCamera`() {
        // The message is the one CameraX emits when a device advertises the
        // camera feature but exposes none; it is what took the app down in the
        // lab.
        val erro = comoOCameraXEntrega(
            CameraUnavailableException(
                CameraUnavailableException.CAMERA_ERROR,
                "Device reporting less cameras than anticipated. Available cameras: 0",
            ),
        )

        assertEquals(FalhaDeCamera.SemCamera, classificarFalhaDeCamera(erro, camerasDoAparelho = 0))
    }

    @Test
    fun `camera tomada por outro app vira CameraOcupada`() {
        val erro = comoOCameraXEntrega(CameraUnavailableException(CameraUnavailableException.CAMERA_IN_USE))

        assertEquals(FalhaDeCamera.CameraOcupada, classificarFalhaDeCamera(erro, camerasDoAparelho = 2))
    }

    @Test
    fun `limite de camaras abertas tambem e disputa, nao falha inesperada`() {
        val erro = comoOCameraXEntrega(CameraUnavailableException(CameraUnavailableException.CAMERA_MAX_IN_USE))

        assertEquals(FalhaDeCamera.CameraOcupada, classificarFalhaDeCamera(erro, camerasDoAparelho = 2))
    }

    @Test
    fun `camera desconectada e tratada como disputa - a acao do usuario e a mesma`() {
        val erro = comoOCameraXEntrega(CameraUnavailableException(CameraUnavailableException.CAMERA_DISCONNECTED))

        assertEquals(FalhaDeCamera.CameraOcupada, classificarFalhaDeCamera(erro, camerasDoAparelho = 1))
    }

    @Test
    fun `camera desligada por politica do aparelho tem causa propria`() {
        val erro = comoOCameraXEntrega(CameraUnavailableException(CameraUnavailableException.CAMERA_DISABLED))

        assertEquals(
            FalhaDeCamera.CameraBloqueadaPeloSistema,
            classificarFalhaDeCamera(erro, camerasDoAparelho = 2),
        )
    }

    @Test
    fun `nao perturbe tambem e bloqueio do sistema`() {
        val erro = comoOCameraXEntrega(
            CameraUnavailableException(CameraUnavailableException.CAMERA_UNAVAILABLE_DO_NOT_DISTURB),
        )

        assertEquals(
            FalhaDeCamera.CameraBloqueadaPeloSistema,
            classificarFalhaDeCamera(erro, camerasDoAparelho = 2),
        )
    }

    @Test
    fun `permissao revogada com a tela aberta aparece como SecurityException no fundo da pilha`() {
        val erro = comoOCameraXEntrega(SecurityException("Lacking privileges to access camera service"))

        assertEquals(FalhaDeCamera.PermissaoNegada, classificarFalhaDeCamera(erro, camerasDoAparelho = 2))
    }

    @Test
    fun `causa desconhecida nao e colapsada - vira FalhaInesperada com o detalhe tecnico`() {
        val erro = comoOCameraXEntrega(IOException("HAL fora do ar"))

        val falha = classificarFalhaDeCamera(erro, camerasDoAparelho = 2)

        assertTrue("esperava FalhaInesperada, veio $falha", falha is FalhaDeCamera.FalhaInesperada)
        val detalhe = (falha as FalhaDeCamera.FalhaInesperada).detalhe
        assertTrue("detalhe deve nomear a causa raiz: $detalhe", detalhe.contains("IOException"))
        assertTrue("detalhe deve trazer a mensagem: $detalhe", detalhe.contains("HAL fora do ar"))
    }

    @Test
    fun `contagem desconhecida (-1) nunca vira SemCamera`() {
        val erro = comoOCameraXEntrega(IOException("qualquer coisa"))

        assertTrue(classificarFalhaDeCamera(erro, camerasDoAparelho = -1) is FalhaDeCamera.FalhaInesperada)
    }

    @Test
    fun `zero cameras vence qualquer outro motivo - insistir nao resolve`() {
        val erro = comoOCameraXEntrega(CameraUnavailableException(CameraUnavailableException.CAMERA_IN_USE))

        assertEquals(FalhaDeCamera.SemCamera, classificarFalhaDeCamera(erro, camerasDoAparelho = 0))
    }

    @Test
    fun `ciclo na cadeia de causas nao trava a classificacao`() {
        val a = RuntimeException("a")
        val b = RuntimeException("b", a)
        a.initCause(b)

        assertTrue(classificarFalhaDeCamera(a, camerasDoAparelho = 1) is FalhaDeCamera.FalhaInesperada)
    }

    // --- a camera that FALLS OVER after opening (CameraState, not an exception) ---

    @Test
    fun `outro app toma a camera com o scanner aberto - vira CameraOcupada`() {
        assertEquals(
            FalhaDeCamera.CameraOcupada,
            classificarErroDeEstadoDaCamera(CameraState.ERROR_CAMERA_IN_USE),
        )
        assertEquals(
            FalhaDeCamera.CameraOcupada,
            classificarErroDeEstadoDaCamera(CameraState.ERROR_MAX_CAMERAS_IN_USE),
        )
    }

    @Test
    fun `camera desligada pelo sistema com o scanner aberto tem causa propria`() {
        assertEquals(
            FalhaDeCamera.CameraBloqueadaPeloSistema,
            classificarErroDeEstadoDaCamera(CameraState.ERROR_CAMERA_DISABLED),
        )
        assertEquals(
            FalhaDeCamera.CameraBloqueadaPeloSistema,
            classificarErroDeEstadoDaCamera(CameraState.ERROR_DO_NOT_DISTURB_MODE_ENABLED),
        )
    }

    @Test
    fun `erro recuperavel nao troca a tela - o CameraX reabre sozinho`() {
        assertNull(classificarErroDeEstadoDaCamera(CameraState.ERROR_OTHER_RECOVERABLE_ERROR))
    }

    @Test
    fun `erro fatal da camera degrada com detalhe, sem inventar causa`() {
        val falha = classificarErroDeEstadoDaCamera(CameraState.ERROR_CAMERA_FATAL_ERROR)

        assertTrue(falha is FalhaDeCamera.FalhaInesperada)
        assertTrue((falha as FalhaDeCamera.FalhaInesperada).detalhe.contains("FATAL"))
    }

    @Test
    fun `negativa de permissao distingue pedir de novo de ir para as configuracoes`() {
        assertEquals(FalhaDeCamera.PermissaoNegada, falhaDePermissao(podePedirNovamente = true))
        assertEquals(FalhaDeCamera.PermissaoBloqueada, falhaDePermissao(podePedirNovamente = false))
    }

    @Test
    fun `cada causa tem texto proprio - nada de mensagem unica de erro generico`() {
        val causas = listOf(
            FalhaDeCamera.PermissaoNegada,
            FalhaDeCamera.PermissaoBloqueada,
            FalhaDeCamera.SemCamera,
            FalhaDeCamera.CameraOcupada,
            FalhaDeCamera.CameraBloqueadaPeloSistema,
            FalhaDeCamera.FalhaInesperada("IOException: x"),
        )

        val titulos = causas.map { it.texto().titulo }
        assertEquals("cada causa precisa de um título próprio", causas.size, titulos.toSet().size)
        causas.forEach { causa ->
            val texto = causa.texto()
            // Every piece of text has to point to a way out: either the action
            // that attacks the cause, or the alternative path (manual server
            // plus username and password).
            assertTrue(
                "a explicação de $causa precisa dizer o que fazer: ${texto.explicacao}",
                texto.acao != null || texto.explicacao.contains("manually"),
            )
        }
    }

    @Test
    fun `sem camera nao oferece acao de retentar - so as saidas`() {
        assertNull(FalhaDeCamera.SemCamera.texto().acao)
    }

    @Test
    fun `falha inesperada carrega o detalhe tecnico para o dono relatar sem adb`() {
        val texto = FalhaDeCamera.FalhaInesperada("IOException: HAL fora do ar").texto()

        assertNotNull(texto.detalheTecnico)
        assertEquals("IOException: HAL fora do ar", texto.detalheTecnico)
    }
}
