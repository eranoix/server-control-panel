package com.vpsmanager.feature.videocall.pip

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
 * The call's floating window (Picture-in-Picture).
 *
 * ## Why this is the model that exists OUTSIDE its own screen
 *
 * Every other screen in this app is only of use while it is open. The call is
 * the only one that has to go on being of use after the person has gone off to
 * do something else — and "something else", during a call about a server,
 * almost always means *opening the terminal in this very app to look at what
 * we are discussing*. Without the floating window that gesture drops the call
 * or makes it invisible; with it, the call becomes a rectangle in the corner
 * and the work carries on.
 *
 * ## The scenario that settles the design is the ERROR one
 *
 * The little window is the **only place** where a drop can be announced while
 * the app is not in the foreground. A notification gets lost among twenty; the
 * window sits on top of whatever the person is looking at. That is why it shows
 * state, not just video: when the connection drops, that is where you read it.
 *
 * ## Two things Android imposes, which change the code
 *
 * 1. **The capability may not exist.** `FEATURE_PICTURE_IN_PICTURE` is
 *    optional; a device without it (or with the mode switched off in settings)
 *    makes `enterPictureInPictureMode` throw. Hence [entrarNaJanela] returns a
 *    boolean and never lets the exception propagate — losing the call because
 *    the little window does not exist would be trading a feature for a defect.
 * 2. **The aspect ratio is mandatory and bounded.** Android refuses ratios
 *    outside roughly 1:2.39 to 2.39:1. 16:9 sits comfortably inside and is the
 *    ratio the camera delivers, so the window neither crops nor distorts the
 *    video.
 */
object JanelaFlutuante {

    /**
     * 16:9 — the video's aspect ratio, within what Android accepts.
     *
     * `Rational` and not a `Float`: the API asks for the exact ratio, and a
     * rounded float near the limit is precisely what makes the call blow up
     * with `IllegalArgumentException` on one device and not on another.
     */
    internal val PROPORCAO = Rational(16, 9)

    private val _naJanela = MutableStateFlow(false)

    /**
     * Whether the call is currently in a floating window.
     *
     * The screen reads this in order to **redraw itself**: a 200 dp window has
     * no room for 48 dp buttons, participant names or a grid. The right content
     * there is one video, one line of state and nothing more — and that is why
     * the mode has to be observable state and not a detail of the system.
     */
    val naJanela: StateFlow<Boolean> = _naJanela.asStateFlow()

    /** Called from the Activity's `onPictureInPictureModeChanged`. */
    fun modoMudou(ativo: Boolean) {
        _naJanela.value = ativo
    }

    /** Whether this device has the floating window. */
    fun disponivel(context: Context): Boolean =
        context.packageManager.hasSystemFeature(PackageManager.FEATURE_PICTURE_IN_PICTURE)

    /**
     * Enters the floating window. Returns `false` if it did not work — and the
     * caller carries on with the call full screen, which is still a call.
     */
    fun entrarNaJanela(context: Context): Boolean {
        val activity = context.activity() ?: return false
        if (!disponivel(context)) return false
        return runCatching {
            activity.enterPictureInPictureMode(
                PictureInPictureParams.Builder().setAspectRatio(PROPORCAO).build(),
            )
        }.getOrDefault(false)
    }

    /** Unwraps the Activity from inside Compose's `ContextWrapper`s. */
    private fun Context.activity(): Activity? {
        var atual: Context? = this
        while (atual is ContextWrapper) {
            if (atual is Activity) return atual
            atual = atual.baseContext
        }
        return null
    }
}

/**
 * While this screen is composed, leaving the app enters the floating window
 * rather than leaving the call in the background.
 *
 * ## Why `setAutoEnterEnabled` and not `onUserLeaveHint`
 *
 * `onUserLeaveHint` is the old route: the app finds out it is leaving and calls
 * `enterPictureInPictureMode` at that instant, which produces a two-beat
 * transition — the screen shrinks after it has already started to leave.
 * `setAutoEnterEnabled` (API 31+, and the minSdk here is 34) tells the system
 * BEFOREHAND: the gesture animation already carries the screen into the window,
 * with no jump.
 *
 * On top of that, `onUserLeaveHint` **does not fire** on some manufacturers'
 * swipe navigation gesture — exactly the gesture most used to switch apps.
 * Relying on it means delivering the floating window to only part of the fleet.
 *
 * The `DisposableEffect` switches auto-enter off when the screen goes away:
 * without that, leaving the app from ANY other screen (the terminal, the file
 * list) would still open the little window for a call that has already ended.
 */
@Composable
fun AutoEntrarNaJanelaFlutuante(habilitado: Boolean = true) {
    val context = LocalContext.current
    val ligado by rememberUpdatedState(habilitado)

    DisposableEffect(context, ligado) {
        val activity = context.activityOuNulo()
        val podeUsar = activity != null && JanelaFlutuante.disponivel(context)
        if (podeUsar) {
            runCatching {
                activity.setPictureInPictureParams(
                    // THE ASPECT RATIO GOES ALONG WITH IT, not just
                    // auto-enter. The PiP parameters are a WHOLE object, not a
                    // patch: sending only `autoEnter` wipes the ratio set
                    // earlier, and the system falls back to its own default —
                    // which crops the video.
                    PictureInPictureParams.Builder()
                        .setAspectRatio(JanelaFlutuante.PROPORCAO)
                        .setAutoEnterEnabled(ligado)
                        .build(),
                )
            }
        }
        onDispose {
            if (podeUsar) {
                runCatching {
                    activity.setPictureInPictureParams(
                        PictureInPictureParams.Builder()
                            .setAspectRatio(JanelaFlutuante.PROPORCAO)
                            .setAutoEnterEnabled(false)
                            .build(),
                    )
                }
            }
        }
    }
}

private fun Context.activityOuNulo(): Activity? {
    var atual: Context? = this
    while (atual is ContextWrapper) {
        if (atual is Activity) return atual
        atual = atual.baseContext
    }
    return null
}
