package com.vpsmanager.feature.notifications.fcm

import android.Manifest
import android.content.Context
import android.content.pm.PackageManager
import android.os.Build
import android.util.Log
import androidx.core.app.NotificationManagerCompat
import androidx.core.content.ContextCompat
import com.google.firebase.messaging.FirebaseMessagingService
import com.google.firebase.messaging.RemoteMessage
import com.vpsmanager.data.push.DeviceIdProvider
import com.vpsmanager.data.push.PushDeviceRepository
import com.vpsmanager.data.videocall.IncomingCallDispatcher
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.launch

private const val TAG = "VpsFirebaseMessaging"
private const val TYPE_INCOMING_CALL = "incoming-call"
private const val TYPE_CALL_ENDED = "call-ended"

/**
 * Receives every push (job outcomes, metric alerts, call ring/hangup) as a data-only FCM
 * message, so this class builds the notification itself and behaves the same whether the
 * process was alive, cold-started or force-stopped.
 *
 * An app can have only one `FirebaseMessagingService`, so call payloads are branched here and
 * handed to [IncomingCallDispatcher]. `type` is untrusted and matched by exact equality; any
 * other value falls through to the ops-alert path, never a fallback ring.
 *
 * Without `google-services.json` no default `FirebaseApp` exists and these callbacks never fire.
 */
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

        // A service cannot request the permission ([PushOnboarding] does after login), only log
        // that it is missing; otherwise `notify` is silently dropped. The check is inline
        // because lint does not recognise a guard hidden behind a helper call.
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
            // Notifications are off in system settings; log it so the drop is not silent.
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


    /**
     * Hands a ring event to [IncomingCallDispatcher]'s handler (Telecom builds the call UI).
     * The server always sends all four fields, so a payload missing one is stale or spoofed
     * and is dropped. Uses `caller_name` because FCM strips the reserved `from` key.
     */
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

    /** Dismisses a ringing or active call as soon as the other side hangs up. */
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

    /** Registers this device's FCM token with `POST /api/mobile/v1/notify/devices`. */
    fun registerToken(token: String) {
        val deviceId = DeviceIdProvider(applicationContext).deviceId()
        scope.launch {
            when (val result = PushDeviceRepository().register(deviceId, token)) {
                is com.vpsmanager.data.push.PushDeviceResult.Success -> Unit
                is com.vpsmanager.data.push.PushDeviceResult.Error -> Log.w(TAG, result.reason)
            }
        }
    }

    companion object {
        /** Process-wide scope: callers have no service instance, and a per-call scope could be dropped mid-request. */
        private val detachedScope = CoroutineScope(SupervisorJob() + Dispatchers.IO)

        /**
         * Registers a token obtained outside the service (from [FirebaseBootstrap]).
         * Needed because `onNewToken` only fires when FCM issues a new token, so a device
         * that already has one would otherwise never register.
         */
        fun registerTokenDetached(context: Context, token: String) {
            val app = context.applicationContext
            val deviceId = DeviceIdProvider(app).deviceId()
            detachedScope.launch {
                when (val result = PushDeviceRepository().register(deviceId, token)) {
                    is com.vpsmanager.data.push.PushDeviceResult.Success -> Unit
                    is com.vpsmanager.data.push.PushDeviceResult.Error -> Log.w(TAG, result.reason)
                }
            }
        }
    }
}
