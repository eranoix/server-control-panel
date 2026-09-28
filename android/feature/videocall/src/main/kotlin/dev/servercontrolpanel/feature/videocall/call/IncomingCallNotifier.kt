package dev.servercontrolpanel.feature.videocall.call

import android.app.NotificationManager
import android.content.Context
import android.content.Intent
import android.content.SharedPreferences
import android.net.Uri
import android.provider.Settings

private const val PREFS_NAME = "panel_incoming_call_notifier"
private const val KEY_BANNER_DISMISSED = "full_screen_intent_banner_dismissed"

interface FullScreenIntentPort {
    fun canUseFullScreenIntent(): Boolean
    fun isBannerDismissed(): Boolean
    fun setBannerDismissed()
}

private class RealFullScreenIntentPort(
    private val notificationManager: NotificationManager,
    private val prefs: SharedPreferences,
) : FullScreenIntentPort {
    override fun canUseFullScreenIntent(): Boolean = notificationManager.canUseFullScreenIntent()
    override fun isBannerDismissed(): Boolean = prefs.getBoolean(KEY_BANNER_DISMISSED, false)
    override fun setBannerDismissed() {
        prefs.edit().putBoolean(KEY_BANNER_DISMISSED, true).apply()
    }
}

class IncomingCallNotifier(private val port: FullScreenIntentPort) {

    constructor(context: Context) : this(
        RealFullScreenIntentPort(
            notificationManager = context.getSystemService(NotificationManager::class.java),
            prefs = context.getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE),
        ),
    )

    fun shouldShowBanner(): Boolean = !port.canUseFullScreenIntent() && !port.isBannerDismissed()

    fun onBannerDismissed() {
        port.setBannerDismissed()
    }

    companion object {
        fun settingsIntent(context: Context): Intent =
            Intent(Settings.ACTION_MANAGE_APP_USE_FULL_SCREEN_INTENT, Uri.fromParts("package", context.packageName, null))
    }
}
