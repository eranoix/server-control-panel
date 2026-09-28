package dev.servercontrolpanel.feature.notifications.fcm

import android.Manifest
import android.content.Context
import android.content.pm.PackageManager
import android.os.Build
import android.util.Log
import androidx.core.app.NotificationManagerCompat
import androidx.core.content.ContextCompat
import com.google.firebase.messaging.FirebaseMessagingService
import com.google.firebase.messaging.RemoteMessage
import dev.servercontrolpanel.data.push.DeviceIdProvider
import dev.servercontrolpanel.data.push.PushDeviceRepository
import dev.servercontrolpanel.data.videocall.IncomingCallDispatcher
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.launch

private const val TAG = "VpsFirebaseMessaging"
private const val TYPE_INCOMING_CALL = "incoming-call"
private const val TYPE_CALL_ENDED = "call-ended"

class VpsFirebaseMessagingService : FirebaseMessagingService() {

    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)

    override fun onNewToken(token: String) {
        super.onNewToken(token)
        registerToken(token)
    }

    override fun onMessageReceived(message: RemoteMessage) {
        super.onMessageReceived(message)
        val data = message.data
        if (data.isEmpty()) return

        when (data["type"]) {
            TYPE_INCOMING_CALL -> {
                dispatchIncomingCall(data)
                return
            }
            TYPE_CALL_ENDED -> {
                dispatchCallEnded(data)
                return
            }
        }

        val manager = NotificationManagerCompat.from(applicationContext)

        val allowed = Build.VERSION.SDK_INT < Build.VERSION_CODES.TIRAMISU ||
            ContextCompat.checkSelfPermission(
                applicationContext,
                Manifest.permission.POST_NOTIFICATIONS,
            ) == PackageManager.PERMISSION_GRANTED
        if (!allowed) {
            Log.w(
                TAG,
                "notification dropped: POST_NOTIFICATIONS not granted " +
                    "(event_type=${data["event_type"]}). It is requested at login.",
            )
            return
        }
        if (!manager.areNotificationsEnabled()) {
            Log.w(
                TAG,
                "notification dropped: notifications disabled in settings " +
                    "(event_type=${data["event_type"]})",
            )
            return
        }

        val notification = ActionableNotificationBuilder.build(applicationContext, data).build()
        val notificationId = ActionableNotificationBuilder.notificationIdFor(
            eventType = data["event_type"].orEmpty(),
            jobId = data["job_id"],
        )
        manager.notify(notificationId, notification)
    }


    private fun dispatchIncomingCall(data: Map<String, String>) {
        val roomId = data["room_id"]
        val roomName = data["room_name"]
        val from = data["caller_name"]
        val callId = data["call_id"]
        if (roomId == null || roomName == null || from == null || callId == null) {
            Log.w(TAG, "incomplete incoming-call payload, ignored: $data")
            return
        }
        val handler = IncomingCallDispatcher.handler
        if (handler == null) {
            Log.w(TAG, "IncomingCallDispatcher has no registered handler; call from $from ignored")
            return
        }
        handler.onIncomingCall(roomId = roomId, roomName = roomName, from = from, callId = callId)
    }

    private fun dispatchCallEnded(data: Map<String, String>) {
        val callId = data["call_id"]
        if (callId == null) {
            Log.w(TAG, "call-ended payload without call_id, ignored: $data")
            return
        }
        val handler = IncomingCallDispatcher.handler
        if (handler == null) {
            Log.w(TAG, "IncomingCallDispatcher has no registered handler; call-ended for $callId ignored")
            return
        }
        handler.onCallEnded(callId)
    }

    fun registerToken(token: String) {
        val deviceId = DeviceIdProvider(applicationContext).deviceId()
        scope.launch {
            when (val result = PushDeviceRepository().register(deviceId, token)) {
                is dev.servercontrolpanel.data.push.PushDeviceResult.Success -> Unit
                is dev.servercontrolpanel.data.push.PushDeviceResult.Error -> Log.w(TAG, result.reason)
            }
        }
    }

    companion object {
        private val detachedScope = CoroutineScope(SupervisorJob() + Dispatchers.IO)

        fun registerTokenDetached(context: Context, token: String) {
            val app = context.applicationContext
            val deviceId = DeviceIdProvider(app).deviceId()
            detachedScope.launch {
                when (val result = PushDeviceRepository().register(deviceId, token)) {
                    is dev.servercontrolpanel.data.push.PushDeviceResult.Success -> Unit
                    is dev.servercontrolpanel.data.push.PushDeviceResult.Error -> Log.w(TAG, result.reason)
                }
            }
        }
    }
}
