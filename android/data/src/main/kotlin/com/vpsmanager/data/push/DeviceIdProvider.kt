package com.vpsmanager.data.push

import android.content.Context
import android.provider.Settings
import java.util.UUID

/** SharedPreferences file every push-onboarding-related flag in this package persists to. */
internal const val PUSH_PREFS_NAME = "vpsm_push_prefs"
private const val KEY_DEVICE_ID = "device_id"

/**
 * Mints this device's `device_id` once and caches it for the lifetime of the install —
 * every one of the registration/preferences/deletion endpoints keys on this exact
 * string, and no other file in the app may read `Settings.Secure.ANDROID_ID` directly.
 *
 * The cached SharedPreferences value always wins over a fresh `ANDROID_ID` read once one
 * exists: `ANDROID_ID` is documented to occasionally change on some OEM factory-reset/restore
 * flows, and a device_id that silently changed underneath an already-registered device would
 * orphan its notification preferences server-side. This deliberately survives app
 * reinstalls too (SharedPreferences data does not survive an uninstall, but `ANDROID_ID` itself
 * does survive one on the same device) — matching another feature's independent choice of the
 * same source for the same shared `/api/mobile/v1/notify/devices` endpoint, so one physical
 * device never ends up registered under two different IDs depending on which code path ran
 * first.
 */
class DeviceIdProvider(context: Context) {
    private val appContext = context.applicationContext
    private val prefs by lazy { appContext.getSharedPreferences(PUSH_PREFS_NAME, Context.MODE_PRIVATE) }

    fun deviceId(): String {
        prefs.getString(KEY_DEVICE_ID, null)?.let { cached -> return cached }
        val minted = mintDeviceId()
        prefs.edit().putString(KEY_DEVICE_ID, minted).apply()
        return minted
    }

    private fun mintDeviceId(): String =
        Settings.Secure.getString(appContext.contentResolver, Settings.Secure.ANDROID_ID)
            ?.takeIf { it.isNotBlank() }
            ?: UUID.randomUUID().toString()
}
