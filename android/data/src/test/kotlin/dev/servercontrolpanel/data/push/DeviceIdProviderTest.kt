package dev.servercontrolpanel.data.push

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

        val second = DeviceIdProvider(context).deviceId()
        assertEquals(first, second)
    }

    @Test
    fun cachedValueWinsEvenIfAndroidIdChanges() {
        val context = RuntimeEnvironment.getApplication()
        Settings.Secure.putString(context.contentResolver, Settings.Secure.ANDROID_ID, "fake-android-id-original")
        val cached = DeviceIdProvider(context).deviceId()

        Settings.Secure.putString(context.contentResolver, Settings.Secure.ANDROID_ID, "fake-android-id-changed")
        val afterChange = DeviceIdProvider(context).deviceId()

        assertEquals(cached, afterChange)
    }
}
