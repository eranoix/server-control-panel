package dev.servercontrolpanel.feature.videocall.pip

import android.app.Activity
import android.app.PictureInPictureParams
import android.content.Context
import android.content.ContextWrapper
import android.content.pm.PackageManager
import android.util.Rational
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.ui.platform.LocalContext
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

/**
 * The call's Picture-in-Picture window, which keeps the call visible (including its connection
 * state) while the user works elsewhere, e.g. in the terminal.
 *
 * PiP is optional: without `FEATURE_PICTURE_IN_PICTURE`, `enterPictureInPictureMode` throws, so
 * [enterWindow] returns a boolean and never propagates the exception. Android only accepts
 * aspect ratios between about 1:2.39 and 2.39:1; 16:9 fits and matches the camera.
 */
object FloatingWindow {

    /** 16:9 as an exact `Rational`; a rounded float near the limit can throw `IllegalArgumentException`. */
    internal val ASPECT_RATIO = Rational(16, 9)

    private val _inWindow = MutableStateFlow(false)

    /** Whether the call is in PiP; the screen observes it to switch to a minimal layout. */
    val inWindow: StateFlow<Boolean> = _inWindow.asStateFlow()

    /** Called from the Activity's `onPictureInPictureModeChanged`. */
    fun modeChanged(active: Boolean) {
        _inWindow.value = active
    }

    /** Whether this device has the floating window. */
    fun available(context: Context): Boolean =
        context.packageManager.hasSystemFeature(PackageManager.FEATURE_PICTURE_IN_PICTURE)

    /** Enters PiP; returns `false` on failure, and the call continues full screen. */
    fun enterWindow(context: Context): Boolean {
        val activity = context.activity() ?: return false
        if (!available(context)) return false
        return runCatching {
            activity.enterPictureInPictureMode(
                PictureInPictureParams.Builder().setAspectRatio(ASPECT_RATIO).build(),
            )
        }.getOrDefault(false)
    }

    /** Unwraps the Activity from inside Compose's `ContextWrapper`s. */
    private fun Context.activity(): Activity? {
        var current: Context? = this
        while (current is ContextWrapper) {
            if (current is Activity) return current
            current = current.baseContext
        }
        return null
    }
}

/**
 * While composed, leaving the app enters PiP instead of backgrounding the call.
 *
 * Uses `setAutoEnterEnabled` rather than `onUserLeaveHint`: it animates smoothly, and
 * `onUserLeaveHint` does not fire for some OEMs' swipe navigation. Auto-enter is turned off on
 * dispose, or leaving from any other screen would open PiP for an ended call.
 */
@Composable
fun AutoEnterFloatingWindow(enabled: Boolean = true) {
    val context = LocalContext.current
    val checked by rememberUpdatedState(enabled)

    DisposableEffect(context, checked) {
        val activity = context.activityOrNull()
        val canUse = activity != null && FloatingWindow.available(context)
        if (canUse) {
            runCatching {
                activity.setPictureInPictureParams(
                    // PiP params replace the whole object, so the aspect ratio must be resent
                    // or the system default crops the video.
                    PictureInPictureParams.Builder()
                        .setAspectRatio(FloatingWindow.ASPECT_RATIO)
                        .setAutoEnterEnabled(checked)
                        .build(),
                )
            }
        }
        onDispose {
            if (canUse) {
                runCatching {
                    activity.setPictureInPictureParams(
                        PictureInPictureParams.Builder()
                            .setAspectRatio(FloatingWindow.ASPECT_RATIO)
                            .setAutoEnterEnabled(false)
                            .build(),
                    )
                }
            }
        }
    }
}

private fun Context.activityOrNull(): Activity? {
    var current: Context? = this
    while (current is ContextWrapper) {
        if (current is Activity) return current
        current = current.baseContext
    }
    return null
}
