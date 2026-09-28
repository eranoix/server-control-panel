package dev.servercontrolpanel.data.push

import android.content.Context
import android.provider.Settings
import java.util.UUID

internal const val PUSH_PREFS_NAME = "panel_push_prefs"
private const val KEY_DEVICE_ID = "device_id"

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
