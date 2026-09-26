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
 * Pure tests of camera failure classification, which decides the action the
 * screen offers (ask permission, open Settings, retry, skip the camera).
 *
 * Exceptions mimic CameraX's real wrapping: `ExecutionException` around
 * `InitializationException` around the `CameraUnavailableException` with the reason.
 */
class PairingCameraTest {

    private fun asCameraXDelivers(cause: Throwable): Throwable =
        ExecutionException(InitializationException(cause))

    @Test
    fun `an emulator without a camera is classified as NoCamera`() {
        // CameraX's message when a device advertises a camera but exposes none.
        val error = asCameraXDelivers(
            CameraUnavailableException(
                CameraUnavailableException.CAMERA_ERROR,
                "Device reporting less cameras than anticipated. Available cameras: 0",
            ),
        )

        assertEquals(CameraFailure.NoCamera, classifyCameraFailure(error, deviceCameras = 0))
    }

    @Test
    fun `a camera taken by another app is CameraInUse`() {
        val error = asCameraXDelivers(CameraUnavailableException(CameraUnavailableException.CAMERA_IN_USE))

        assertEquals(CameraFailure.CameraInUse, classifyCameraFailure(error, deviceCameras = 2))
    }

    @Test
    fun `the open-camera limit is also contention, not an unexpected failure`() {
        val error = asCameraXDelivers(CameraUnavailableException(CameraUnavailableException.CAMERA_MAX_IN_USE))

        assertEquals(CameraFailure.CameraInUse, classifyCameraFailure(error, deviceCameras = 2))
    }

    @Test
    fun `a disconnected camera is treated as contention since the user action is the same`() {
        val error = asCameraXDelivers(CameraUnavailableException(CameraUnavailableException.CAMERA_DISCONNECTED))

        assertEquals(CameraFailure.CameraInUse, classifyCameraFailure(error, deviceCameras = 1))
    }

    @Test
    fun `a camera disabled by device policy has its own cause`() {
        val error = asCameraXDelivers(CameraUnavailableException(CameraUnavailableException.CAMERA_DISABLED))

        assertEquals(
            CameraFailure.CameraBlockedBySystem,
            classifyCameraFailure(error, deviceCameras = 2),
        )
    }

    @Test
    fun `do not disturb is also a system block`() {
        val error = asCameraXDelivers(
            CameraUnavailableException(CameraUnavailableException.CAMERA_UNAVAILABLE_DO_NOT_DISTURB),
        )

        assertEquals(
            CameraFailure.CameraBlockedBySystem,
            classifyCameraFailure(error, deviceCameras = 2),
        )
    }

    @Test
    fun `permission revoked while open shows up as a nested SecurityException`() {
        val error = asCameraXDelivers(SecurityException("Lacking privileges to access camera service"))

        assertEquals(CameraFailure.PermissionDenied, classifyCameraFailure(error, deviceCameras = 2))
    }

    @Test
    fun `an unknown cause is not collapsed and becomes UnexpectedFailure with detail`() {
        val error = asCameraXDelivers(IOException("HAL fora do ar"))

        val failure = classifyCameraFailure(error, deviceCameras = 2)

        assertTrue("expected UnexpectedFailure, got $failure", failure is CameraFailure.UnexpectedFailure)
        val detail = (failure as CameraFailure.UnexpectedFailure).detail
        assertTrue("detail must name the root cause: $detail", detail.contains("IOException"))
        assertTrue("detail must include the message: $detail", detail.contains("HAL fora do ar"))
    }

    @Test
    fun `an unknown camera count (-1) is never NoCamera`() {
        val error = asCameraXDelivers(IOException("anything"))

        assertTrue(classifyCameraFailure(error, deviceCameras = -1) is CameraFailure.UnexpectedFailure)
    }

    @Test
    fun `zero cameras beats any other reason since retrying cannot help`() {
        val error = asCameraXDelivers(CameraUnavailableException(CameraUnavailableException.CAMERA_IN_USE))

        assertEquals(CameraFailure.NoCamera, classifyCameraFailure(error, deviceCameras = 0))
    }

    @Test
    fun `a cycle in the cause chain does not hang classification`() {
        val a = RuntimeException("a")
        val b = RuntimeException("b", a)
        a.initCause(b)

        assertTrue(classifyCameraFailure(a, deviceCameras = 1) is CameraFailure.UnexpectedFailure)
    }

    // Failures after the camera opened arrive as CameraState errors, not exceptions.

    @Test
    fun `another app taking the camera while scanning is CameraInUse`() {
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
    fun `the system disabling the camera while scanning has its own cause`() {
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
    fun `a recoverable error keeps the screen since CameraX reopens by itself`() {
        assertNull(classifyCameraStateError(CameraState.ERROR_OTHER_RECOVERABLE_ERROR))
    }

    @Test
    fun `a fatal camera error degrades with detail, without inventing a cause`() {
        val failure = classifyCameraStateError(CameraState.ERROR_CAMERA_FATAL_ERROR)

        assertTrue(failure is CameraFailure.UnexpectedFailure)
        assertTrue((failure as CameraFailure.UnexpectedFailure).detail.contains("FATAL"))
    }

    @Test
    fun `a permission denial distinguishes asking again from going to Settings`() {
        assertEquals(CameraFailure.PermissionDenied, permissionFailure(canAskAgain = true))
        assertEquals(CameraFailure.PermissionBlocked, permissionFailure(canAskAgain = false))
    }

    @Test
    fun `each cause has its own text, no single generic error message`() {
        val causes = listOf(
            CameraFailure.PermissionDenied,
            CameraFailure.PermissionBlocked,
            CameraFailure.NoCamera,
            CameraFailure.CameraInUse,
            CameraFailure.CameraBlockedBySystem,
            CameraFailure.UnexpectedFailure("IOException: x"),
        )

        val titles = causes.map { it.text().title }
        assertEquals("each cause needs its own title", causes.size, titles.toSet().size)
        causes.forEach { cause ->
            val text = cause.text()
            // Each text must offer a way out: an action, or the manual server plus password path.
            assertTrue(
                "the explanation for $cause must say what to do: ${text.explanation}",
                text.action != null || text.explanation.contains("manually"),
            )
        }
    }

    @Test
    fun `no camera offers no retry action, only the alternatives`() {
        assertNull(CameraFailure.NoCamera.text().action)
    }

    @Test
    fun `an unexpected failure carries technical detail to report without adb`() {
        val text = CameraFailure.UnexpectedFailure("IOException: HAL down").text()

        assertNotNull(text.technicalDetail)
        assertEquals("IOException: HAL down", text.technicalDetail)
    }
}
