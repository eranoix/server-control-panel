package dev.servercontrolpanel.feature.auth

import android.Manifest
import android.app.Activity
import android.content.ActivityNotFoundException
import android.content.Context
import android.content.ContextWrapper
import android.content.Intent
import android.content.pm.PackageManager
import android.hardware.camera2.CameraAccessException
import android.hardware.camera2.CameraManager
import android.net.Uri
import android.provider.Settings
import androidx.camera.core.CameraSelector
import androidx.camera.core.CameraUnavailableException
import androidx.camera.lifecycle.ProcessCameraProvider
import androidx.core.content.ContextCompat
import com.google.common.util.concurrent.ListenableFuture
import kotlinx.coroutines.CancellableContinuation
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException

internal sealed interface CameraFailure {

    data object PermissionDenied : CameraFailure

    data object PermissionBlocked : CameraFailure

    data object NoCamera : CameraFailure

    data object CameraInUse : CameraFailure

    data object CameraBlockedBySystem : CameraFailure

    data class UnexpectedFailure(val detail: String) : CameraFailure
}

internal sealed interface ScannerState {

    data object Checking : ScannerState

    data object RequestingPermission : ScannerState

    data class Scanning(val useFrontCamera: Boolean) : ScannerState

    data class Degraded(val failure: CameraFailure) : ScannerState
}

internal sealed interface CameraReadiness {
    data class Ready(val useFrontCamera: Boolean) : CameraReadiness
    data class Unavailable(val failure: CameraFailure) : CameraReadiness
}

internal fun interface CameraCheck {
    suspend fun check(context: Context): CameraReadiness
}

internal class CameraEnvironment(
    val hasPermission: (Context) -> Boolean = ::hasCameraPermission,
    val canAskPermissionAgain: (Context) -> Boolean = ::canAskCameraPermissionAgain,
    val cameraCheck: CameraCheck = CameraXCheck,
)

internal object CameraXCheck : CameraCheck {

    override suspend fun check(context: Context): CameraReadiness {
        val cameras = countDeviceCameras(context)
        if (cameras == 0) return CameraReadiness.Unavailable(CameraFailure.NoCamera)

        val provider = try {
            ProcessCameraProvider.getInstance(context).await(context)
        } catch (e: CancellationException) {
            throw e
        } catch (t: Throwable) {
            return CameraReadiness.Unavailable(classifyCameraFailure(t, cameras))
        }

        return try {
            when {
                provider.hasCamera(CameraSelector.DEFAULT_BACK_CAMERA) ->
                    CameraReadiness.Ready(useFrontCamera = false)
                provider.hasCamera(CameraSelector.DEFAULT_FRONT_CAMERA) ->
                    CameraReadiness.Ready(useFrontCamera = true)
                else -> CameraReadiness.Unavailable(CameraFailure.NoCamera)
            }
        } catch (e: CancellationException) {
            throw e
        } catch (t: Throwable) {
            CameraReadiness.Unavailable(classifyCameraFailure(t, cameras))
        }
    }
}

private suspend fun <T> ListenableFuture<T>.await(context: Context): T =
    suspendCancellableCoroutine { continuation: CancellableContinuation<T> ->
        addListener(
            {
                try {
                    continuation.resume(get())
                } catch (t: Throwable) {
                    continuation.resumeWithException(t)
                }
            },
            ContextCompat.getMainExecutor(context),
        )
        continuation.invokeOnCancellation { cancel(false) }
    }

internal fun countDeviceCameras(context: Context): Int =
    try {
        (context.getSystemService(Context.CAMERA_SERVICE) as? CameraManager)?.cameraIdList?.size ?: -1
    } catch (e: CameraAccessException) {
        -1
    } catch (e: IllegalArgumentException) {
        -1
    } catch (e: AssertionError) {
        -1
    }

internal fun classifyCameraFailure(error: Throwable, deviceCameras: Int): CameraFailure {
    val causes = causeChain(error)

    if (causes.any { it is SecurityException }) return CameraFailure.PermissionDenied

    if (deviceCameras == 0) return CameraFailure.NoCamera

    val unavailable = causes.filterIsInstance<CameraUnavailableException>().firstOrNull()
        ?: return CameraFailure.UnexpectedFailure(summarizeCause(causes))

    return when (unavailable.reason) {
        CameraUnavailableException.CAMERA_IN_USE,
        CameraUnavailableException.CAMERA_MAX_IN_USE,
        CameraUnavailableException.CAMERA_DISCONNECTED,
        -> CameraFailure.CameraInUse

        CameraUnavailableException.CAMERA_DISABLED,
        CameraUnavailableException.CAMERA_UNAVAILABLE_DO_NOT_DISTURB,
        -> CameraFailure.CameraBlockedBySystem

        else -> CameraFailure.UnexpectedFailure(summarizeCause(causes))
    }
}

internal fun classifyCameraStateError(code: Int): CameraFailure? = when (code) {
    androidx.camera.core.CameraState.ERROR_CAMERA_IN_USE,
    androidx.camera.core.CameraState.ERROR_MAX_CAMERAS_IN_USE,
    -> CameraFailure.CameraInUse

    androidx.camera.core.CameraState.ERROR_CAMERA_DISABLED,
    androidx.camera.core.CameraState.ERROR_DO_NOT_DISTURB_MODE_ENABLED,
    -> CameraFailure.CameraBlockedBySystem

    androidx.camera.core.CameraState.ERROR_OTHER_RECOVERABLE_ERROR -> null

    androidx.camera.core.CameraState.ERROR_STREAM_CONFIG ->
        CameraFailure.UnexpectedFailure("CameraState.ERROR_STREAM_CONFIG: stream configuration rejected")

    androidx.camera.core.CameraState.ERROR_CAMERA_FATAL_ERROR ->
        CameraFailure.UnexpectedFailure("CameraState.ERROR_CAMERA_FATAL_ERROR: the camera needs to be restarted")

    else -> CameraFailure.UnexpectedFailure("CameraState error $code")
}

private fun causeChain(error: Throwable): List<Throwable> {
    val visited = mutableListOf<Throwable>()
    var current: Throwable? = error
    while (current != null && visited.size < 12 && visited.none { it === current }) {
        visited += current
        current = current.cause
    }
    return visited
}

private fun summarizeCause(causes: List<Throwable>): String {
    val root = causes.lastOrNull() ?: return "unknown cause"
    val message = root.message?.takeIf { it.isNotBlank() }?.let { ": $it" }.orEmpty()
    return "${root.javaClass.simpleName}$message".take(400)
}

internal fun hasCameraPermission(context: Context): Boolean =
    ContextCompat.checkSelfPermission(context, Manifest.permission.CAMERA) == PackageManager.PERMISSION_GRANTED

internal fun canAskCameraPermissionAgain(context: Context): Boolean {
    val activity = context.findActivity() ?: return false
    return activity.shouldShowRequestPermissionRationale(Manifest.permission.CAMERA)
}

internal fun permissionFailure(canAskAgain: Boolean): CameraFailure =
    if (canAskAgain) CameraFailure.PermissionDenied else CameraFailure.PermissionBlocked

internal fun openAppSettings(context: Context): Boolean = try {
    context.startActivity(
        Intent(
            Settings.ACTION_APPLICATION_DETAILS_SETTINGS,
            Uri.fromParts("package", context.packageName, null),
        ).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
    )
    true
} catch (e: ActivityNotFoundException) {
    false
} catch (e: SecurityException) {
    false
}

private fun Context.findActivity(): Activity? {
    var current: Context? = this
    while (current is ContextWrapper) {
        if (current is Activity) return current
        current = current.baseContext
    }
    return null
}
