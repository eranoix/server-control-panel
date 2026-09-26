package com.vpsmanager.app.update

import android.app.ActivityOptions
import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.util.Log
import com.vpsmanager.app.MainActivity
import com.vpsmanager.app.R

/**
 * Brings the person back after the app updates itself.
 *
 * ## The problem
 *
 * Updating **kills the process**: Android replaces the package and takes
 * down whatever was running. Whoever tapped "Update" sees the app simply
 * disappear from the screen.
 *
 * The route the person was on has already been saved by
 * [com.vpsmanager.data.update.OndeEuEstava], so **opening it again gives
 * back exactly the right screen**. What is missing is the reopening.
 *
 * ## Why there are TWO paths, and why the second is not redundancy
 *
 * 1. **Reopening on its own.** Tried first, and **measured**: on the lab
 *    emulator (Android 16) Android refuses SILENTLY — no exception, no
 *    log, and the app does not come to the front. It is the background
 *    Activity-start restriction, under which a just-replaced process does
 *    not qualify for an exemption. It stays as an attempt because it costs
 *    nothing and may get through on another version or another
 *    manufacturer; never as the plan.
 * 2. **A one-tap notification.** Always published. It is the path that
 *    depends on no manufacturer's permission, and it leads to the SAME
 *    screen.
 *
 * If (1) works, (2) is one extra notification the person dismisses. If (1)
 * is refused, (2) is the only way back without hunting for the icon. The
 * asymmetry of cost between the two failures is what justifies keeping
 * both.
 *
 * ## Why not a foreground service
 *
 * It would be the "right" way to guarantee the return, and it is
 * disproportionate: keeping a service alive all the time to cover the five
 * seconds of an update spends battery every day for an event that happens
 * once per version.
 */
class VoltaDepoisDaAtualizacao : BroadcastReceiver() {

    override fun onReceive(context: Context, intent: Intent) {
        if (intent.action != Intent.ACTION_MY_PACKAGE_REPLACED) return

        val paraOApp = Intent(context, MainActivity::class.java)
            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TOP)

        // (1) the attempt to reopen on its own.
        //
        // MEASURED on the lab emulator (Android 16): the receiver runs, this
        // call does NOT throw, and even so the app does not come to the front
        // — the launcher stays the top screen. The background Activity-start
        // restriction refuses SILENTLY.
        //
        // The attempt stays because it costs nothing and may get through on
        // another version or another manufacturer. What does not stay is the
        // illusion of knowing whether it worked: `startActivity` returns with
        // no error in both cases, so any flag from here would be invention —
        // and that is exactly what this code did before the measurement.
        tentarReabrir(context, paraOApp)

        // (2) the notification is the REAL path, and it always goes out.
        //
        // If (1) worked, it is a notification the person never even sees,
        // because the app is already in front. If it did not — which is what
        // the measurement shows — it is the only way back without hunting for
        // the icon on the home screen.
        publicarAviso(context, paraOApp)
    }

    /**
     * Tries to bring the app back on its own.
     *
     * A plain `context.startActivity` does NOT get through: a receiver has no
     * visible window, and the restriction refuses silently (measured). What
     * Android's documentation does open up is a `PendingIntent`'s **privilege
     * chain**: it is enough for the CREATOR or the SENDER to declare the
     * intent.
     *
     * With `targetSdk` 35 or higher, the creator's declaration
     * (`pendingIntentCreatorBackgroundActivityStartMode`) became MANDATORY —
     * and that is precisely what was missing. Declaring nothing, the system
     * blocks and logs `Background activity launch blocked!` under the
     * `ActivityTaskManager` tag, without throwing anything at us.
     *
     * We declare both sides: it costs nothing and each covers a different path
     * through the chain.
     */
    private fun tentarReabrir(context: Context, paraOApp: Intent) {
        val opcoesDeCriacao = ActivityOptions.makeBasic().apply {
            pendingIntentCreatorBackgroundActivityStartMode =
                ActivityOptions.MODE_BACKGROUND_ACTIVITY_START_ALLOWED
        }
        val pendente = PendingIntent.getActivity(
            context,
            CODIGO_REABRIR,
            paraOApp,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
            opcoesDeCriacao.toBundle(),
        )
        val opcoesDeEnvio = ActivityOptions.makeBasic().apply {
            pendingIntentBackgroundActivityStartMode =
                ActivityOptions.MODE_BACKGROUND_ACTIVITY_START_ALLOWED
        }
        try {
            pendente.send(opcoesDeEnvio.toBundle())
        } catch (e: PendingIntent.CanceledException) {
            Log.i(TAG, "reabrir sozinho: PendingIntent cancelado: ${e.message}")
        }
    }

    private fun publicarAviso(context: Context, paraOApp: Intent) {
        val manager = context.getSystemService(NotificationManager::class.java) ?: return
        manager.createNotificationChannel(
            NotificationChannel(
                CANAL,
                "App updates",
                // LOW on purpose: it makes no sound and does not vibrate. It
                // is an invitation to come back, not an alert — and a sound
                // for "I have finished updating" would be noise.
                NotificationManager.IMPORTANCE_LOW,
            ),
        )

        val toque = PendingIntent.getActivity(
            context,
            0,
            paraOApp,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )

        val aviso = Notification.Builder(context, CANAL)
            .setSmallIcon(R.mipmap.ic_launcher)
            .setContentTitle("Server Control Panel updated")
            // The text says what the tap DOES, and not "update complete":
            // whoever was interrupted wants to know how to get back, not that it
            // finished.
            .setContentText("Tap to return to the screen you were on.")
            .setContentIntent(toque)
            .setAutoCancel(true)
            .build()

        manager.notify(ID_AVISO, aviso)
    }

    private companion object {
        const val TAG = "VoltaDepoisDaAtualizacao"
        const val CANAL = "atualizacao_concluida"
        const val ID_AVISO = 4711
        const val CODIGO_REABRIR = 4712
    }
}
