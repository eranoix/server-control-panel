package com.vpsmanager.feature.videocall.call

import android.app.NotificationManager
import android.content.Context
import android.content.Intent
import android.content.SharedPreferences
import android.net.Uri
import android.provider.Settings

private const val PREFS_NAME = "vpsm_incoming_call_notifier"
private const val KEY_BANNER_DISMISSED = "full_screen_intent_banner_dismissed"

/**
 * Narrow seam over the two `NotificationManager`/`SharedPreferences` members this class needs —
 * matches [TelecomAccountPort]'s established pattern in this package so [IncomingCallNotifier]
 * stays testable with hand-rolled fakes instead of a real Robolectric-backed system service.
 */
interface FullScreenIntentPort {
    /** False once the user has manually revoked the Settings toggle (API 34+) — see class doc. */
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

/**
 * This app is exempt from Google Play's automated `USE_FULL_SCREEN_INTENT` revocation
 * (self-hosted F-Droid distribution, not Play Store) — but a user can still flip the per-app
 * Settings toggle themselves. [shouldShowBanner] is the one place that degraded state
 * (a call would ring silently as a heads-up notification instead of the full lock-screen UI,
 * with no other visible explanation) gets surfaced, deep-linking to the exact settings screen via
 * [settingsIntent]. "One-time, dismissible": once the user dismisses it, it does not reappear —
 * re-revoking is a deliberate user action they were already shown the consequence of.
 */
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
        /** Deep-links straight to this app's full-screen-intent toggle, never the generic app-info screen. */
        fun settingsIntent(context: Context): Intent =
            Intent(Settings.ACTION_MANAGE_APP_USE_FULL_SCREEN_INTENT, Uri.fromParts("package", context.packageName, null))
    }
}
