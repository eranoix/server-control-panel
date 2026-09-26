package com.vpsmanager.feature.notifications.fcm

import android.Manifest
import android.app.NotificationManager
import com.google.firebase.messaging.RemoteMessage
import com.vpsmanager.data.videocall.IncomingCallDispatcher
import com.vpsmanager.data.videocall.IncomingCallHandler
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Robolectric
import org.robolectric.RobolectricTestRunner
import org.robolectric.RuntimeEnvironment
import org.robolectric.Shadows.shadowOf

private class FakeIncomingCallHandler : IncomingCallHandler {
    data class IncomingCall(val roomId: String, val roomName: String, val from: String, val callId: String)

    var incomingCalls = mutableListOf<IncomingCall>()
        private set
    var endedCallIds = mutableListOf<String>()
        private set

    override fun onIncomingCall(roomId: String, roomName: String, from: String, callId: String) {
        incomingCalls += IncomingCall(roomId, roomName, from, callId)
    }

    override fun onCallEnded(callId: String) {
        endedCallIds += callId
    }
}

private fun remoteMessageWithData(data: Map<String, String>): RemoteMessage =
    RemoteMessage.Builder("vpsm-test@fcm.googleapis.com").setData(data).build()

@RunWith(RobolectricTestRunner::class)
class VpsFirebaseMessagingServiceTest {

    // Robolectric.setupService (not a bare constructor call) attaches a real Context — needed
    // even for the branches that never touch it directly, since onMessageReceived's ops-alert
    // fallthrough (existing behavior) reads applicationContext.
    private val service = Robolectric.setupService(VpsFirebaseMessagingService::class.java)

    @After
    fun tearDown() {
        IncomingCallDispatcher.handler = null
    }

    @Test
    fun `incoming-call payload dispatches to the registered IncomingCallHandler, not a notification`() {
        val fake = FakeIncomingCallHandler()
        IncomingCallDispatcher.handler = fake

        service.onMessageReceived(
            remoteMessageWithData(
                mapOf(
                    "type" to "incoming-call",
                    "room_id" to "r1",
                    "room_name" to "Sala",
                    "caller_name" to "alice",
                    "call_id" to "c1",
                    "ts" to "1234567890",
                ),
            ),
        )

        assertEquals(1, fake.incomingCalls.size)
        assertEquals(FakeIncomingCallHandler.IncomingCall("r1", "Sala", "alice", "c1"), fake.incomingCalls[0])
    }

    @Test
    fun `call-ended payload dispatches onCallEnded with the call id`() {
        val fake = FakeIncomingCallHandler()
        IncomingCallDispatcher.handler = fake

        service.onMessageReceived(
            remoteMessageWithData(mapOf("type" to "call-ended", "room_id" to "r1", "call_id" to "c1")),
        )

        assertEquals(listOf("c1"), fake.endedCallIds)
    }

    @Test
    fun `unknown type is ignored by the call dispatcher and never reaches it`() {
        val fake = FakeIncomingCallHandler()
        IncomingCallDispatcher.handler = fake

        service.onMessageReceived(
            remoteMessageWithData(mapOf("type" to "some-future-type", "event_type" to "job.failed")),
        )

        assertTrue(fake.incomingCalls.isEmpty())
        assertTrue(fake.endedCallIds.isEmpty())
    }

    @Test
    fun `a payload with no handler registered does not crash`() {
        assertNull(IncomingCallDispatcher.handler)

        // Must not throw even though nothing is registered to receive it.
        service.onMessageReceived(
            remoteMessageWithData(
                mapOf("type" to "incoming-call", "room_id" to "r1", "room_name" to "Sala", "caller_name" to "alice", "call_id" to "c1"),
            ),
        )
    }

    @Test
    fun `incoming-call payload missing a required field is dropped, not partially handled`() {
        val fake = FakeIncomingCallHandler()
        IncomingCallDispatcher.handler = fake

        service.onMessageReceived(
            remoteMessageWithData(mapOf("type" to "incoming-call", "room_id" to "r1")),
        )

        assertTrue(fake.incomingCalls.isEmpty())
    }

    @Test
    fun `ops-alert payload without a type field still builds an ActionableNotification as before`() {
        val fake = FakeIncomingCallHandler()
        IncomingCallDispatcher.handler = fake
        val context = RuntimeEnvironment.getApplication()

        // Not calling the service's own onMessageReceived here (it would post a system
        // notification); asserting instead — as ActionableNotificationBuilderTest already
        // does — that the existing ops-alert builder is unaffected by the new type branch.
        val notification = ActionableNotificationBuilder.build(
            context,
            mapOf("event_type" to "job.failed", "job_id" to "abc123"),
        ).build()

        assertTrue(fake.incomingCalls.isEmpty())
        assertTrue(notification.actions?.isNotEmpty() == true)
    }

    @Test
    fun `sem POST_NOTIFICATIONS a notificacao e descartada, nao postada`() {
        // The defect this test pins down: the app declared the permission in
        // the manifest and NEVER asked for it (nothing triggered the runtime
        // request outside the file browser), so on a fresh install `notify`
        // was swallowed by the system with no exception and no trace. Today
        // the path is explicit and logged — and what asks for it is
        // `OnboardingDePush`, at login.
        val app = RuntimeEnvironment.getApplication()
        shadowOf(app).denyPermissions(Manifest.permission.POST_NOTIFICATIONS)
        val service = Robolectric.setupService(VpsFirebaseMessagingService::class.java)

        service.onMessageReceived(
            remoteMessageWithData(mapOf("event_type" to "job.failed", "job_id" to "abc123")),
        )

        assertTrue(
            "nada pode ser postado sem permissao",
            shadowOf(app.getSystemService(NotificationManager::class.java))
                .allNotifications.isEmpty(),
        )
    }

    @Test
    fun `com POST_NOTIFICATIONS concedida a notificacao e postada`() {
        val app = RuntimeEnvironment.getApplication()
        shadowOf(app).grantPermissions(Manifest.permission.POST_NOTIFICATIONS)
        NotificationChannels.ensureChannels(app)
        val service = Robolectric.setupService(VpsFirebaseMessagingService::class.java)

        service.onMessageReceived(
            remoteMessageWithData(mapOf("event_type" to "job.failed", "job_id" to "abc123")),
        )

        assertEquals(
            1,
            shadowOf(app.getSystemService(NotificationManager::class.java))
                .allNotifications.size,
        )
    }
}
