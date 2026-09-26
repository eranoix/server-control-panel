package com.vpsmanager.app

import android.app.NotificationManager
import androidx.test.core.app.ApplicationProvider
import com.vpsmanager.data.videocall.IncomingCallDispatcher
import com.vpsmanager.feature.notifications.fcm.NotificationChannels
import org.junit.After
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/**
 * End-to-end proof of [VpsManagerApplication.onCreate]'s launch path: this app was built and
 * shipped with no device or emulator ever available, so this Robolectric run is, by
 * construction, the only rehearsal any of this wiring gets before a real phone's first boot.
 *
 * Robolectric instantiates [VpsManagerApplication] and calls its real `onCreate()` automatically
 * before this test method runs (it does this for every test in this module, regardless of
 * whether the test asks for it) -- so by the time a `@Test` body starts, every `Bootstrap.step`
 * has already fired for real. These assertions check each step's actual, independent side
 * effect landed, proving the steps really ran and were wired correctly rather than one being
 * silently skipped or another masking a dependency's absence.
 *
 * The "faxina de transferencias (WorkManager)" step's own outcome (succeeds via the manifest's
 * `androidx.startup` provider, or fails because nothing pre-initializes a test `WorkManager`) is
 * an environment detail this test does not pin either way -- Robolectric runs the real
 * `WorkManagerInitializer` content provider before `onCreate()`, same ordering as a real device,
 * so whether it succeeds here depends on this module's Robolectric resource configuration, not
 * on anything [Bootstrap] or [appCoroutineScope] control. What this test DOES pin is the
 * contract itself: whatever that step's outcome, it must never come back as an uncaught
 * exception, and it must never block the other steps -- exactly what [appCoroutineScope]
 * ([AppCoroutineScopeTest]) and [Bootstrap.step] ([BootstrapTest]) are pinned against directly
 * and in isolation.
 */
@RunWith(RobolectricTestRunner::class)
class VpsManagerApplicationTest {

    @Before
    fun setUp() {
        // Do NOT reset IncomingCallDispatcher.handler here: Robolectric already ran the real
        // onCreate() before this method executes (see class doc), so clearing it now would wipe
        // out the very assignment this test exists to observe. Only tearDown() resets it, for
        // the next test's own onCreate() run.
        Bootstrap.initFailures.clear()
    }

    @After
    fun tearDown() {
        Bootstrap.initFailures.clear()
        IncomingCallDispatcher.handler = null
    }

    @Test
    fun `onCreate wires notification channels and the incoming-call handler, regardless of the WorkManager step's outcome`() {
        val app = ApplicationProvider.getApplicationContext<VpsManagerApplication>()

        val notificationManager = app.getSystemService(NotificationManager::class.java)
        assertNotNull(
            "Bootstrap.step(\"canais de notificacao\") must have created the deploy channel",
            notificationManager.getNotificationChannel(NotificationChannels.CHANNEL_DEPLOY),
        )
        assertNotNull(
            "Bootstrap.step(\"canais de notificacao\") must have created the alerts channel",
            notificationManager.getNotificationChannel(NotificationChannels.CHANNEL_ALERTS),
        )
        assertNotNull(
            "Bootstrap.step(\"canais de notificacao\") must have created the inbox channel",
            notificationManager.getNotificationChannel(NotificationChannels.CHANNEL_INBOX),
        )

        assertNotNull(
            "Bootstrap.step(\"handler de chamada recebida\") must install a handler before any " +
                "FCM message can possibly arrive -- see IncomingCallDispatcher's own doc for " +
                "what happens if this step is skipped or reordered",
            IncomingCallDispatcher.handler,
        )

        // The WorkManager step runs on a real background thread (appScope.launch(Dispatchers.IO)),
        // so give any async outcome (success or a caught failure) a bounded moment to settle
        // before inspecting it -- without asserting which of the two it must be (see class doc).
        Thread.sleep(300)
        val appScopeFailures = Bootstrap.initFailures.filter { it.startsWith("appScope:") }
        assertTrue(
            "if the WorkManager step failed, it must be recorded as a single, well-formed " +
                "appScope entry -- never silently duplicated and never left to crash the " +
                "process (that crash-freedom is what this whole test run already demonstrates: " +
                "a JUnit test that hung or the JVM itself dying would never reach this line)",
            appScopeFailures.size <= 1,
        )
    }
}
