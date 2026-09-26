package com.vpsmanager.data.push

import android.provider.Settings
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.RuntimeEnvironment

@RunWith(RobolectricTestRunner::class)
class DeviceIdProviderTest {

    @Test
    fun mintsAndCachesOnFirstAccess() {
        val context = RuntimeEnvironment.getApplication()
        Settings.Secure.putString(context.contentResolver, Settings.Secure.ANDROID_ID, "fake-android-id-1")

        val first = DeviceIdProvider(context).deviceId()
        assertFalse(first.isBlank())

        // Fresh instance, same SharedPreferences — must return the exact same cached value.
        val second = DeviceIdProvider(context).deviceId()
        assertEquals(first, second)
    }

    @Test
    fun cachedValueWinsEvenIfAndroidIdChanges() {
        val context = RuntimeEnvironment.getApplication()
        Settings.Secure.putString(context.contentResolver, Settings.Secure.ANDROID_ID, "fake-android-id-original")
        val cached = DeviceIdProvider(context).deviceId()

        // Simulate an OEM factory-reset/restore flow changing ANDROID_ID mid-life.
        Settings.Secure.putString(context.contentResolver, Settings.Secure.ANDROID_ID, "fake-android-id-changed")
        val afterChange = DeviceIdProvider(context).deviceId()

        assertEquals(cached, afterChange)
    }
}
