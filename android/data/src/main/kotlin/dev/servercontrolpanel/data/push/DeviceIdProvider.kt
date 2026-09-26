package dev.servercontrolpanel.data.push

import android.content.Context
import android.provider.Settings
import java.util.UUID

/** SharedPreferences file for the push onboarding flags in this package. */
internal const val PUSH_PREFS_NAME = "panel_push_prefs"
private const val KEY_DEVICE_ID = "device_id"

/**
 * Mints this device's `device_id` once and caches it for the install. The
 * registration, preferences and deletion endpoints key on this exact string, and
 * no other file may read `Settings.Secure.ANDROID_ID` directly.
 *
 * The cached value always wins over a fresh `ANDROID_ID` read, since some OEM
 * restore flows change `ANDROID_ID` and a changed id would orphan the server-side
 * preferences. Using `ANDROID_ID` as the source keeps the same id across
 * reinstalls, so one device is never registered twice on `/notify/devices`.
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
