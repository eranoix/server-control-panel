package dev.servercontrolpanel.app

import android.app.NotificationManager
import androidx.test.core.app.ApplicationProvider
import dev.servercontrolpanel.data.videocall.IncomingCallDispatcher
import dev.servercontrolpanel.feature.notifications.fcm.NotificationChannels
import org.junit.After
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/**
 * Exercises [PanelApplication.onCreate]'s real launch path. Robolectric runs the real
 * `onCreate()` before each test, so every `Bootstrap.step` has already fired; the assertions
 * check each step's independent side effect.
 *
 * Whether the WorkManager step succeeds depends on the Robolectric environment and is not
 * pinned. What is pinned is that its outcome never becomes an uncaught exception and never
 * blocks the other steps.
 */
@RunWith(RobolectricTestRunner::class)
class PanelApplicationTest {

    @Before
    fun setUp() {
        // Do not reset IncomingCallDispatcher.handler here: onCreate() already set it, and that
        // assignment is what the test observes. tearDown() resets it.
        Bootstrap.initFailures.clear()
    }

    @After
    fun tearDown() {
        Bootstrap.initFailures.clear()
        IncomingCallDispatcher.handler = null
    }

    @Test
    fun `onCreate wires notification channels and the incoming-call handler, regardless of the WorkManager step's outcome`() {
        val app = ApplicationProvider.getApplicationContext<PanelApplication>()

        val notificationManager = app.getSystemService(NotificationManager::class.java)
        assertNotNull(
            "the notification channels step must have created the deploy channel",
            notificationManager.getNotificationChannel(NotificationChannels.CHANNEL_DEPLOY),
        )
        assertNotNull(
            "the notification channels step must have created the alerts channel",
            notificationManager.getNotificationChannel(NotificationChannels.CHANNEL_ALERTS),
        )
        assertNotNull(
            "the notification channels step must have created the inbox channel",
            notificationManager.getNotificationChannel(NotificationChannels.CHANNEL_INBOX),
        )

        assertNotNull(
            "the incoming call handler step must install a handler before any " +
                "FCM message can arrive; see IncomingCallDispatcher for " +
                "what happens if this step is skipped or reordered",
            IncomingCallDispatcher.handler,
        )

        // The WorkManager step runs on a background thread; give it a bounded moment to settle.
        Thread.sleep(300)
        val appScopeFailures = Bootstrap.initFailures.filter { it.startsWith("appScope:") }
        assertTrue(
            "if the WorkManager step failed, it must be recorded as a single " +
                "appScope entry, never duplicated and never crashing the " +
                "process (reaching this line already shows that the process " +
                "did not crash)",
            appScopeFailures.size <= 1,
        )
    }
}
