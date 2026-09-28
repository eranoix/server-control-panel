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

object FloatingWindow {

    internal val ASPECT_RATIO = Rational(16, 9)

    private val _inWindow = MutableStateFlow(false)

    val inWindow: StateFlow<Boolean> = _inWindow.asStateFlow()

    fun modeChanged(active: Boolean) {
        _inWindow.value = active
    }

    fun available(context: Context): Boolean =
        context.packageManager.hasSystemFeature(PackageManager.FEATURE_PICTURE_IN_PICTURE)

    fun enterWindow(context: Context): Boolean {
        val activity = context.activity() ?: return false
        if (!available(context)) return false
        return runCatching {
            activity.enterPictureInPictureMode(
                PictureInPictureParams.Builder().setAspectRatio(ASPECT_RATIO).build(),
            )
        }.getOrDefault(false)
    }

    private fun Context.activity(): Activity? {
        var current: Context? = this
        while (current is ContextWrapper) {
            if (current is Activity) return current
            current = current.baseContext
        }
        return null
    }
}

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
