package com.vpsmanager.feature.auth

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
 * Why the pairing camera did not open — and, by consequence, what the
 * operator has to do about it.
 *
 * These causes are kept apart on purpose. A generic "failed to open the
 * camera" tells nobody whether the way out is granting a permission, ending
 * the video call that is holding the camera, or giving up on the camera and
 * configuring the server by hand — and each of those is the right answer to a
 * different cause.
 *
 * None of them is fatal: [PairingScanScreen] renders every one of them as a
 * screen with a way out. Before this classification existed, the failure rose
 * as an unhandled exception from inside a `Runnable` on the main executor and
 * **killed the process** — see the comment on [CameraXCheck].
 */
internal sealed interface CameraFailure {

    /** Permission denied, but the system still allows asking again. */
    data object PermissionDenied : CameraFailure

    /**
     * Denied for good ("don't ask again", or denied twice). The system starts
     * swallowing any new request silently, so insisting on the dialog is a
     * loop that never goes anywhere: the only way out is the app's Settings
     * screen.
     */
    data object PermissionBlocked : CameraFailure

    /** The device exposes no camera at all. Insisting does not help. */
    data object NoCamera : CameraFailure

    /**
     * The camera exists but another app has taken it — this very app does
     * video calls, so the contention is a real scenario, not a hypothetical.
     */
    data object CameraInUse : CameraFailure

    /** Switched off by device policy or by Do Not Disturb mode. */
    data object CameraBlockedBySystem : CameraFailure

    /**
     * Anything else. [detail] is the technical text of the root cause, shown
     * on screen because the server's owner has no `adb` to work out for
     * themselves what happened — if the app does not say, nobody says.
     */
    data class UnexpectedFailure(val detail: String) : CameraFailure
}

/** What the scanner screen is doing right now. */
internal sealed interface ScannerState {

    /** Checking permission and availability — nothing to show yet. */
    data object Checking : ScannerState

    /** The system permission dialog is in front of the user. */
    data object RequestingPermission : ScannerState

    /** Camera open; [useFrontCamera] when there is no usable rear one. */
    data class Scanning(val useFrontCamera: Boolean) : ScannerState

    /** No camera, with explanation and exit. Never a blank screen, never a crash. */
    data class Degraded(val failure: CameraFailure) : ScannerState
}

/** Result of [CameraCheck.check]. */
internal sealed interface CameraReadiness {
    data class Ready(val useFrontCamera: Boolean) : CameraReadiness
    data class Unavailable(val failure: CameraFailure) : CameraReadiness
}

/**
 * The seam between the screen and CameraX.
 *
 * It exists so that the FAILURE path is testable on the JVM: a fake returning
 * [CameraReadiness.Unavailable] reproduces "provider failed", "camera
 * busy" or "device has no camera" without depending on any hardware — not
 * even the emulator, whose camera configuration another session may switch on
 * or off underneath the test. Every risky decision happens in here, BEFORE
 * any `PreviewView` exists, and what comes out is a value, never an exception.
 */
internal fun interface CameraCheck {
    suspend fun check(context: Context): CameraReadiness
}

/**
 * Everything [PairingScanScreen] asks of the device, in one injectable place.
 * The no-argument constructor is the production behaviour; tests swap the
 * pieces one by one.
 */
internal class CameraEnvironment(
    val hasPermission: (Context) -> Boolean = ::hasCameraPermission,
    val canAskPermissionAgain: (Context) -> Boolean = ::canAskCameraPermissionAgain,
    val cameraCheck: CameraCheck = CameraXCheck,
)

/**
 * The real implementation: asks for the [ProcessCameraProvider] and only
 * returns [CameraReadiness.Ready] when there really is a camera for the
 * scanner to use.
 *
 * The defect this replaces: the previous code called
 * `cameraProviderFuture.get()` inside a `Runnable` handed to the main
 * executor, with no `try`. On a device (or emulator) where CameraX
 * initialisation fails, the future completes with an `ExecutionException`
 * wrapping an `InitializationException` → `CameraUnavailableException`
 * ("Device reporting less cameras than anticipated … Available cameras: 0").
 * Thrown from inside a `Runnable` on the main thread, that exception has
 * nobody to catch it: it goes to the `UncaughtExceptionHandler` and **closes
 * the app**. In an app for operating servers, closing on its own is not an
 * option.
 *
 * Here the future is awaited in a coroutine, every failure becomes a value
 * via [classifyCameraFailure], and the selector choice accounts for
 * devices with no rear camera (falling back to the front one instead of
 * failing).
 */
internal object CameraXCheck : CameraCheck {

    override suspend fun check(context: Context): CameraReadiness {
        val cameras = countDeviceCameras(context)
        // An honest short circuit: with no camera at all there is nothing
        // to try, and bringing CameraX up just to hear "0 cameras" is noise.
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
                // A device with only a front camera still reads a QR code —
                // worse ergonomics, but a path that works instead of an error.
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
 * Awaits a [ListenableFuture] without blocking the main thread and without
 * pulling in `kotlinx-coroutines-guava` (a whole dependency for one
 * function). The exception that comes out of here is the same one `get()`
 * would throw — wrapped in an `ExecutionException` — because
 * [classifyCameraFailure] is the one that unwraps it.
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
 * How many cameras the device REALLY exposes.
 *
 * `PackageManager.hasSystemFeature(FEATURE_CAMERA_ANY)` cannot be used in its
 * place: it is exactly the discrepancy CameraX itself complains about
 * ("Ensure virtual camera configuration matches supported camera features as
 * reported by PackageManager#hasSystemFeature") — the lab emulator advertises
 * the `android.hardware.camera.any` feature and can still have zero cameras.
 * The `cameraIdList` is the list that tells the truth, and it does not need
 * the CAMERA permission.
 *
 * Returns `-1` when there was no way to know; in that case CameraX decides.
 */
internal fun countDeviceCameras(context: Context): Int =
    try {
        (context.getSystemService(Context.CAMERA_SERVICE) as? CameraManager)?.cameraIdList?.size ?: -1
    } catch (e: CameraAccessException) {
        -1
    } catch (e: IllegalArgumentException) {
        // Some manufacturers throw from here when the camera service is
        // down; "I don't know" is the honest answer, not "zero".
        -1
    } catch (e: AssertionError) {
        // CameraManager.getCameraIdList() throws AssertionError on ROMs with
        // a broken HAL. Never let it rise: it would be the same crash again.
        -1
    }

/**
 * Translates the exception CameraX spat out into the cause the operator
 * understands.
 *
 * Pure on purpose — it is the piece the tests exercise cause by cause, with
 * no Compose, no Robolectric and no hardware.
 *
 * [deviceCameras] comes from [countDeviceCameras]; `-1` means
 * "unknown" and does not become [CameraFailure.NoCamera].
 */
internal fun classifyCameraFailure(error: Throwable, deviceCameras: Int): CameraFailure {
    val causes = causeChain(error)

    // Permission revoked in Settings with the screen already open: CameraX
    // runs into a SecurityException deep down the stack.
    if (causes.any { it is SecurityException }) return CameraFailure.PermissionDenied

    if (deviceCameras == 0) return CameraFailure.NoCamera

    val unavailable = causes.filterIsInstance<CameraUnavailableException>().firstOrNull()
        ?: return CameraFailure.UnexpectedFailure(summarizeCause(causes))

    return when (unavailable.reason) {
        CameraUnavailableException.CAMERA_IN_USE,
        CameraUnavailableException.CAMERA_MAX_IN_USE,
        // DISCONNECTED in practice means another client has taken the
        // camera: the user's action is the same as CAMERA_IN_USE — release
        // and try again.
        CameraUnavailableException.CAMERA_DISCONNECTED,
        -> CameraFailure.CameraInUse

        CameraUnavailableException.CAMERA_DISABLED,
        CameraUnavailableException.CAMERA_UNAVAILABLE_DO_NOT_DISTURB,
        -> CameraFailure.CameraBlockedBySystem

        else -> CameraFailure.UnexpectedFailure(summarizeCause(causes))
    }
}

/**
 * Translates an [androidx.camera.core.CameraState] error — the camera that
 * was already open and dropped afterwards.
 *
 * This path does NOT go through any exception: CameraX reports losing the
 * camera at runtime through an observable `LiveData`, so a `try` around
 * `bindToLifecycle` sees nothing. And it is precisely the likeliest scenario
 * in this app: the operator opens the scanner and a video call (from this
 * very app or another one) takes the camera halfway through. Without this,
 * the preview simply froze black with no explanation.
 *
 * Returns `null` when the screen should not be degraded: CameraX reopens the
 * camera by itself on recoverable errors, and swapping the screen for an
 * error card in that case would be flicker on top of something that is
 * already sorting itself out.
 */
internal fun classifyCameraStateError(code: Int): CameraFailure? = when (code) {
    androidx.camera.core.CameraState.ERROR_CAMERA_IN_USE,
    androidx.camera.core.CameraState.ERROR_MAX_CAMERAS_IN_USE,
    -> CameraFailure.CameraInUse

    androidx.camera.core.CameraState.ERROR_CAMERA_DISABLED,
    androidx.camera.core.CameraState.ERROR_DO_NOT_DISTURB_MODE_ENABLED,
    -> CameraFailure.CameraBlockedBySystem

    // Recoverable: CameraX tries again of its own accord.
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
 * The technical text shown in [CameraFailure.UnexpectedFailure]: the deepest
 * cause, which is the one that says something — `ExecutionException` and
 * `InitializationException` are only wrapping.
 */
private fun summarizeCause(causes: List<Throwable>): String {
    val root = causes.lastOrNull() ?: return "unknown cause"
    val message = root.message?.takeIf { it.isNotBlank() }?.let { ": $it" }.orEmpty()
    return "${root.javaClass.simpleName}$message".take(400)
}

/** Camera permission granted right now? */
internal fun hasCameraPermission(context: Context): Boolean =
    ContextCompat.checkSelfPermission(context, Manifest.permission.CAMERA) == PackageManager.PERMISSION_GRANTED

/**
 * After a denial, does the system still allow asking again?
 *
 * `shouldShowRequestPermissionRationale` is only trustworthy AFTER a denial —
 * which is why it is consulted only when the dialog returns. `false` there
 * means "denied for good": any new request comes back denied immediately,
 * without showing any dialog at all. With no Activity around (a test context,
 * say) the safe answer is `false`, which leads to Settings instead of
 * insisting on a dialog that never appears.
 */
internal fun canAskCameraPermissionAgain(context: Context): Boolean {
    val activity = context.findActivity() ?: return false
    return activity.shouldShowRequestPermissionRationale(Manifest.permission.CAMERA)
}

/** The [CameraFailure] matching a permission denial. */
internal fun permissionFailure(canAskAgain: Boolean): CameraFailure =
    if (canAskAgain) CameraFailure.PermissionDenied else CameraFailure.PermissionBlocked

/**
 * Opens this app's Settings screen — the only way out when the permission has
 * been denied for good. Returns `false` if even that did not work (a ROM with
 * no app-details Activity), so the screen can tell the truth instead of
 * pretending it opened. Never throws: that is how this flow used to bring the
 * app down.
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
