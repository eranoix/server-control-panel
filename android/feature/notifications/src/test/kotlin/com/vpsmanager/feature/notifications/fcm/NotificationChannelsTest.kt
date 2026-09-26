package com.vpsmanager.feature.notifications.fcm

import android.app.NotificationManager
import org.junit.Assert.assertEquals
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.RuntimeEnvironment

@RunWith(RobolectricTestRunner::class)
class NotificationChannelsTest {

    @Test
    fun `ensureChannels creates deploy and alerts channels at IMPORTANCE_HIGH`() {
        val context = RuntimeEnvironment.getApplication()
        NotificationChannels.ensureChannels(context)

        val manager = context.getSystemService(NotificationManager::class.java)
        assertEquals(
            NotificationManager.IMPORTANCE_HIGH,
            manager.getNotificationChannel(NotificationChannels.CHANNEL_DEPLOY).importance,
        )
        assertEquals(
            NotificationManager.IMPORTANCE_HIGH,
            manager.getNotificationChannel(NotificationChannels.CHANNEL_ALERTS).importance,
        )
    }

    @Test
    fun `ensureChannels is idempotent`() {
        val context = RuntimeEnvironment.getApplication()

        NotificationChannels.ensureChannels(context)
        NotificationChannels.ensureChannels(context)

        val manager = context.getSystemService(NotificationManager::class.java)
        assertEquals(3, manager.notificationChannels.size)
    }

    @Test
    fun `channelFor maps job and metric prefixes to the correct channel`() {
        assertEquals(NotificationChannels.CHANNEL_DEPLOY, NotificationChannels.channelFor("job.failed"))
        assertEquals(NotificationChannels.CHANNEL_ALERTS, NotificationChannels.channelFor("metric.threshold"))
        assertEquals(NotificationChannels.CHANNEL_INBOX, NotificationChannels.channelFor("unknown.thing"))
    }
}
