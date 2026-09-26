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
class PairingCameraTest {

    private fun asCameraXDelivers(cause: Throwable): Throwable =
        ExecutionException(InitializationException(cause))

    @Test
    fun `emulador sem camera - a falha real que fechava o app vira SemCamera`() {
        // The message is the one CameraX emits when a device advertises the
        // camera feature but exposes none; it is what took the app down in the
        // lab.
        val error = asCameraXDelivers(
            CameraUnavailableException(
                CameraUnavailableException.CAMERA_ERROR,
                "Device reporting less cameras than anticipated. Available cameras: 0",
            ),
        )

        assertEquals(CameraFailure.NoCamera, classifyCameraFailure(error, deviceCameras = 0))
    }

    @Test
    fun `camera tomada por outro app vira CameraOcupada`() {
        val error = asCameraXDelivers(CameraUnavailableException(CameraUnavailableException.CAMERA_IN_USE))

        assertEquals(CameraFailure.CameraInUse, classifyCameraFailure(error, deviceCameras = 2))
    }

    @Test
    fun `limite de camaras abertas tambem e disputa, nao falha inesperada`() {
        val error = asCameraXDelivers(CameraUnavailableException(CameraUnavailableException.CAMERA_MAX_IN_USE))

        assertEquals(CameraFailure.CameraInUse, classifyCameraFailure(error, deviceCameras = 2))
    }

    @Test
    fun `camera desconectada e tratada como disputa - a acao do usuario e a mesma`() {
        val error = asCameraXDelivers(CameraUnavailableException(CameraUnavailableException.CAMERA_DISCONNECTED))

        assertEquals(CameraFailure.CameraInUse, classifyCameraFailure(error, deviceCameras = 1))
    }

    @Test
    fun `camera desligada por politica do aparelho tem causa propria`() {
        val error = asCameraXDelivers(CameraUnavailableException(CameraUnavailableException.CAMERA_DISABLED))

        assertEquals(
            CameraFailure.CameraBlockedBySystem,
            classifyCameraFailure(error, deviceCameras = 2),
        )
    }

    @Test
    fun `nao perturbe tambem e bloqueio do sistema`() {
        val error = asCameraXDelivers(
            CameraUnavailableException(CameraUnavailableException.CAMERA_UNAVAILABLE_DO_NOT_DISTURB),
        )

        assertEquals(
            CameraFailure.CameraBlockedBySystem,
            classifyCameraFailure(error, deviceCameras = 2),
        )
    }

    @Test
    fun `permissao revogada com a tela aberta aparece como SecurityException no fundo da pilha`() {
        val error = asCameraXDelivers(SecurityException("Lacking privileges to access camera service"))

        assertEquals(CameraFailure.PermissionDenied, classifyCameraFailure(error, deviceCameras = 2))
    }

    @Test
    fun `causa desconhecida nao e colapsada - vira FalhaInesperada com o detalhe tecnico`() {
        val error = asCameraXDelivers(IOException("HAL fora do ar"))

        val failure = classifyCameraFailure(error, deviceCameras = 2)

        assertTrue("esperava FalhaInesperada, veio $failure", failure is CameraFailure.UnexpectedFailure)
        val detail = (failure as CameraFailure.UnexpectedFailure).detail
        assertTrue("detalhe deve nomear a causa raiz: $detail", detail.contains("IOException"))
        assertTrue("detalhe deve trazer a mensagem: $detail", detail.contains("HAL fora do ar"))
    }

    @Test
    fun `contagem desconhecida (-1) nunca vira SemCamera`() {
        val error = asCameraXDelivers(IOException("qualquer coisa"))

        assertTrue(classifyCameraFailure(error, deviceCameras = -1) is CameraFailure.UnexpectedFailure)
    }

    @Test
    fun `zero cameras vence qualquer outro motivo - insistir nao resolve`() {
        val error = asCameraXDelivers(CameraUnavailableException(CameraUnavailableException.CAMERA_IN_USE))

        assertEquals(CameraFailure.NoCamera, classifyCameraFailure(error, deviceCameras = 0))
    }

    @Test
    fun `ciclo na cadeia de causas nao trava a classificacao`() {
        val a = RuntimeException("a")
        val b = RuntimeException("b", a)
        a.initCause(b)

        assertTrue(classifyCameraFailure(a, deviceCameras = 1) is CameraFailure.UnexpectedFailure)
    }

    // --- a camera that FALLS OVER after opening (CameraState, not an exception) ---

    @Test
    fun `outro app toma a camera com o scanner aberto - vira CameraOcupada`() {
        assertEquals(
            CameraFailure.CameraInUse,
            classifyCameraStateError(CameraState.ERROR_CAMERA_IN_USE),
        )
        assertEquals(
            CameraFailure.CameraInUse,
            classifyCameraStateError(CameraState.ERROR_MAX_CAMERAS_IN_USE),
        )
    }

    @Test
    fun `camera desligada pelo sistema com o scanner aberto tem causa propria`() {
        assertEquals(
            CameraFailure.CameraBlockedBySystem,
            classifyCameraStateError(CameraState.ERROR_CAMERA_DISABLED),
        )
        assertEquals(
            CameraFailure.CameraBlockedBySystem,
            classifyCameraStateError(CameraState.ERROR_DO_NOT_DISTURB_MODE_ENABLED),
        )
    }

    @Test
    fun `erro recuperavel nao troca a tela - o CameraX reabre sozinho`() {
        assertNull(classifyCameraStateError(CameraState.ERROR_OTHER_RECOVERABLE_ERROR))
    }

    @Test
    fun `erro fatal da camera degrada com detalhe, sem inventar causa`() {
        val failure = classifyCameraStateError(CameraState.ERROR_CAMERA_FATAL_ERROR)

        assertTrue(failure is CameraFailure.UnexpectedFailure)
        assertTrue((failure as CameraFailure.UnexpectedFailure).detail.contains("FATAL"))
    }

    @Test
    fun `negativa de permissao distingue pedir de novo de ir para as configuracoes`() {
        assertEquals(CameraFailure.PermissionDenied, permissionFailure(canAskAgain = true))
        assertEquals(CameraFailure.PermissionBlocked, permissionFailure(canAskAgain = false))
    }

    @Test
    fun `cada causa tem texto proprio - nada de mensagem unica de erro generico`() {
        val causes = listOf(
            CameraFailure.PermissionDenied,
            CameraFailure.PermissionBlocked,
            CameraFailure.NoCamera,
            CameraFailure.CameraInUse,
            CameraFailure.CameraBlockedBySystem,
            CameraFailure.UnexpectedFailure("IOException: x"),
        )

        val titles = causes.map { it.text().title }
        assertEquals("cada causa precisa de um título próprio", causes.size, titles.toSet().size)
        causes.forEach { cause ->
            val text = cause.text()
            // Every piece of text has to point to a way out: either the action
            // that attacks the cause, or the alternative path (manual server
            // plus username and password).
            assertTrue(
                "a explicação de $cause precisa dizer o que fazer: ${text.explanation}",
                text.action != null || text.explanation.contains("manually"),
            )
        }
    }

    @Test
    fun `sem camera nao oferece acao de retentar - so as saidas`() {
        assertNull(CameraFailure.NoCamera.text().action)
    }

    @Test
    fun `falha inesperada carrega o detalhe tecnico para o dono relatar sem adb`() {
        val text = CameraFailure.UnexpectedFailure("IOException: HAL fora do ar").text()

        assertNotNull(text.technicalDetail)
        assertEquals("IOException: HAL fora do ar", text.technicalDetail)
    }
}
