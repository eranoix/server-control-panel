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

/**
 * Why the pairing camera did not open, kept as distinct causes because each
 * needs a different user action (grant permission, free the camera, or set up
 * the server manually). None is fatal: [PairingScanScreen] renders each with a
 * way out.
 */
internal sealed interface CameraFailure {

    /** Permission denied, but the system still allows asking again. */
    data object PermissionDenied : CameraFailure

    /**
     * Permanently denied. The system silently rejects new requests, so the only
     * way out is the app's Settings screen.
     */
    data object PermissionBlocked : CameraFailure

    /** The device exposes no camera at all. */
    data object NoCamera : CameraFailure

    /** Another app (e.g. a video call) holds the camera. */
    data object CameraInUse : CameraFailure

    /** Switched off by device policy or by Do Not Disturb mode. */
    data object CameraBlockedBySystem : CameraFailure

    /**
     * Anything else. [detail] is the root cause's technical text, shown on
     * screen because the owner has no `adb` to find it.
     */
    data class UnexpectedFailure(val detail: String) : CameraFailure
}

/** What the scanner screen is doing right now. */
internal sealed interface ScannerState {

    /** Checking permission and availability; nothing to show yet. */
    data object Checking : ScannerState

    /** The system permission dialog is in front of the user. */
    data object RequestingPermission : ScannerState

    /** Camera open; [useFrontCamera] when there is no usable rear one. */
    data class Scanning(val useFrontCamera: Boolean) : ScannerState

    /** No camera: an explanation and a way out, never a blank screen or a crash. */
    data class Degraded(val failure: CameraFailure) : ScannerState
}

/** Result of [CameraCheck.check]. */
internal sealed interface CameraReadiness {
    data class Ready(val useFrontCamera: Boolean) : CameraReadiness
    data class Unavailable(val failure: CameraFailure) : CameraReadiness
}

/**
 * Seam between the screen and CameraX, so failure paths are testable on the JVM.
 * All risky work happens here before any `PreviewView` exists, and the result
 * is a value, never an exception.
 */
internal fun interface CameraCheck {
    suspend fun check(context: Context): CameraReadiness
}

/**
 * Everything [PairingScanScreen] asks of the device, injectable for tests.
 * The defaults are the production behavior.
 */
internal class CameraEnvironment(
    val hasPermission: (Context) -> Boolean = ::hasCameraPermission,
    val canAskPermissionAgain: (Context) -> Boolean = ::canAskCameraPermissionAgain,
    val cameraCheck: CameraCheck = CameraXCheck,
)

/**
 * Production check: returns [CameraReadiness.Ready] only when a usable camera exists.
 *
 * The provider future is awaited in a coroutine and every failure becomes a
 * value via [classifyCameraFailure]. Calling `get()` in a main-thread
 * `Runnable` would let CameraX init failures crash the app uncaught.
 * Falls back to the front camera when there is no rear one.
 */
internal object CameraXCheck : CameraCheck {

    override suspend fun check(context: Context): CameraReadiness {
        val cameras = countDeviceCameras(context)
        // No camera at all: skip starting CameraX.
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
                // A front-only device can still read a QR code.
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

/**
 * Awaits a [ListenableFuture] without blocking and without `kotlinx-coroutines-guava`.
 * Rethrows exactly what `get()` throws (an `ExecutionException`); [classifyCameraFailure] unwraps it.
 */
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

/**
 * How many cameras the device actually exposes, via `cameraIdList` (no CAMERA
 * permission needed). `hasSystemFeature(FEATURE_CAMERA_ANY)` is unreliable:
 * emulators can advertise the feature with zero cameras.
 *
 * Returns `-1` when unknown, leaving the decision to CameraX.
 */
internal fun countDeviceCameras(context: Context): Int =
    try {
        (context.getSystemService(Context.CAMERA_SERVICE) as? CameraManager)?.cameraIdList?.size ?: -1
    } catch (e: CameraAccessException) {
        -1
    } catch (e: IllegalArgumentException) {
        // Some OEMs throw here when the camera service is down; answer "unknown", not zero.
        -1
    } catch (e: AssertionError) {
        // getCameraIdList() throws AssertionError on ROMs with a broken HAL; never let it crash.
        -1
    }

/**
 * Maps a CameraX exception to a [CameraFailure]. Pure, so tests cover each cause.
 *
 * [deviceCameras] comes from [countDeviceCameras]; `-1` means unknown and never
 * becomes [CameraFailure.NoCamera].
 */
internal fun classifyCameraFailure(error: Throwable, deviceCameras: Int): CameraFailure {
    val causes = causeChain(error)

    // Permission revoked while the screen is open surfaces as a nested SecurityException.
    if (causes.any { it is SecurityException }) return CameraFailure.PermissionDenied

    if (deviceCameras == 0) return CameraFailure.NoCamera

    val unavailable = causes.filterIsInstance<CameraUnavailableException>().firstOrNull()
        ?: return CameraFailure.UnexpectedFailure(summarizeCause(causes))

    return when (unavailable.reason) {
        CameraUnavailableException.CAMERA_IN_USE,
        CameraUnavailableException.CAMERA_MAX_IN_USE,
        // DISCONNECTED usually means another client took the camera; same user action as IN_USE.
        CameraUnavailableException.CAMERA_DISCONNECTED,
        -> CameraFailure.CameraInUse

        CameraUnavailableException.CAMERA_DISABLED,
        CameraUnavailableException.CAMERA_UNAVAILABLE_DO_NOT_DISTURB,
        -> CameraFailure.CameraBlockedBySystem

        else -> CameraFailure.UnexpectedFailure(summarizeCause(causes))
    }
}

/**
 * Maps an [androidx.camera.core.CameraState] error, i.e. an open camera lost at
 * runtime (e.g. a video call takes it). CameraX reports this via `LiveData`, not
 * an exception, so without it the preview would just freeze.
 *
 * Returns `null` for recoverable errors, which CameraX reopens by itself.
 */
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

/** Causes from the outside in, without repeats (cycle guard) and capped. */
private fun causeChain(error: Throwable): List<Throwable> {
    val visited = mutableListOf<Throwable>()
    var current: Throwable? = error
    while (current != null && visited.size < 12 && visited.none { it === current }) {
        visited += current
        current = current.cause
    }
    return visited
}

/**
 * Technical text for [CameraFailure.UnexpectedFailure]: the deepest cause, since
 * the outer exceptions are only wrappers.
 */
private fun summarizeCause(causes: List<Throwable>): String {
    val root = causes.lastOrNull() ?: return "unknown cause"
    val message = root.message?.takeIf { it.isNotBlank() }?.let { ": $it" }.orEmpty()
    return "${root.javaClass.simpleName}$message".take(400)
}

/** Whether the camera permission is currently granted. */
internal fun hasCameraPermission(context: Context): Boolean =
    ContextCompat.checkSelfPermission(context, Manifest.permission.CAMERA) == PackageManager.PERMISSION_GRANTED

/**
 * After a denial, whether the system still allows asking again.
 *
 * `shouldShowRequestPermissionRationale` is only reliable after a denial, so call
 * it only when the dialog returns. With no Activity the answer is `false`, which
 * leads to Settings rather than a dialog that never appears.
 */
internal fun canAskCameraPermissionAgain(context: Context): Boolean {
    val activity = context.findActivity() ?: return false
    return activity.shouldShowRequestPermissionRationale(Manifest.permission.CAMERA)
}

/** The [CameraFailure] matching a permission denial. */
internal fun permissionFailure(canAskAgain: Boolean): CameraFailure =
    if (canAskAgain) CameraFailure.PermissionDenied else CameraFailure.PermissionBlocked

/**
 * Opens this app's Settings screen, the only way out after a permanent denial.
 * Returns `false` if no app-details Activity exists. Never throws.
 */
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

/** Unwraps the Activity from inside Compose's [ContextWrapper]s. */
private fun Context.findActivity(): Activity? {
    var current: Context? = this
    while (current is ContextWrapper) {
        if (current is Activity) return current
        current = current.baseContext
    }
    return null
}
