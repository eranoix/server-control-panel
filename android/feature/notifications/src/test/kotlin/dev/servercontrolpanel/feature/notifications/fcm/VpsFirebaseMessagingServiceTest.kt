package dev.servercontrolpanel.feature.notifications.fcm

import android.Manifest
import android.app.NotificationManager
import com.google.firebase.messaging.RemoteMessage
import dev.servercontrolpanel.data.videocall.IncomingCallDispatcher
import dev.servercontrolpanel.data.videocall.IncomingCallHandler
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
    RemoteMessage.Builder("panel-test@fcm.googleapis.com").setData(data).build()

@RunWith(RobolectricTestRunner::class)
class VpsFirebaseMessagingServiceTest {

    // setupService attaches a real Context, which the ops-alert fallthrough reads.
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
                    "room_name" to "Room",
                    "caller_name" to "alice",
                    "call_id" to "c1",
                    "ts" to "1234567890",
                ),
            ),
        )

        assertEquals(1, fake.incomingCalls.size)
        assertEquals(FakeIncomingCallHandler.IncomingCall("r1", "Room", "alice", "c1"), fake.incomingCalls[0])
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
                mapOf("type" to "incoming-call", "room_id" to "r1", "room_name" to "Room", "caller_name" to "alice", "call_id" to "c1"),
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

        // Calls the builder directly instead of onMessageReceived, which would post a real notification.
        val notification = ActionableNotificationBuilder.build(
            context,
            mapOf("event_type" to "job.failed", "job_id" to "abc123"),
        ).build()

        assertTrue(fake.incomingCalls.isEmpty())
        assertTrue(notification.actions?.isNotEmpty() == true)
    }

    @Test
    fun `without POST_NOTIFICATIONS the notification is dropped, not posted`() {
        // Without the permission `notify` is silently swallowed, so the service must check and log.
        val app = RuntimeEnvironment.getApplication()
        shadowOf(app).denyPermissions(Manifest.permission.POST_NOTIFICATIONS)
        val service = Robolectric.setupService(VpsFirebaseMessagingService::class.java)

        service.onMessageReceived(
            remoteMessageWithData(mapOf("event_type" to "job.failed", "job_id" to "abc123")),
        )

        assertTrue(
            "nothing may be posted without permission",
            shadowOf(app.getSystemService(NotificationManager::class.java))
                .allNotifications.isEmpty(),
        )
    }

    @Test
    fun `with POST_NOTIFICATIONS granted the notification is posted`() {
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
