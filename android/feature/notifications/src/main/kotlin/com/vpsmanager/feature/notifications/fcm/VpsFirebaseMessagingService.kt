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
 * Receives every push this app is sent — deploy/job outcomes and metric alerts, and videocall
 * ring/hangup events — as a data-only FCM message (never a notification-payload message: for
 * the ops-alert path this class must build and post the [android.app.Notification] itself via
 * [ActionableNotificationBuilder], so the exact same code path runs whether the process was
 * already alive or FCM cold-started it, and works identically with the app fully force-stopped;
 * the call path hands off to Telecom instead — see below).
 *
 * An app may register only one `FirebaseMessagingService` (a second `<service>` declaration
 * would silently shadow this one) — `type=incoming-call`/`type=call-ended` payloads are
 * therefore branched on here, not in a second messaging service, and handed off to
 * [IncomingCallDispatcher]'s registered [com.vpsmanager.data.videocall.IncomingCallHandler]
 * (`:feature-videocall`'s Telecom integration) instead of being treated as an ops alert.
 * `type` is untrusted input from the caller's own FCM payload — matched by exact equality
 * only, and any other/missing value falls straight through to the existing ops-alert path
 * unchanged, never a fallback "ring anyway".
 *
 * `google-services.json` is not provisioned in this repo yet (see `gradle/libs.versions.toml`'s
 * `firebaseBom` comment) — until a human adds it, `FirebaseMessaging`/this service's own
 * lifecycle callbacks never fire because no default `FirebaseApp` exists to bind them to. The
 * class itself compiles and is fully wired regardless, so no code changes are needed once the
 * project is provisioned — only the manifest/`google-services.json` addition.
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

        val gerente = NotificationManagerCompat.from(applicationContext)

        // A messaging service has no screen: from here there is no way to
        // REQUEST the permission, only to note that it is missing. The one
        // that asks is [OnboardingDePush], at the post-login moment. Without
        // this check the `notify` was swallowed by the system with no
        // exception and no trace — and for a good while the permission was
        // always missing, because nothing in the app ever got round to
        // requesting `POST_NOTIFICATIONS`.
        // The check is INLINE, and not via the helper function: lint does not
        // follow a method call to recognise the guard, and a guard it cannot
        // see comes back as a build error.
        val permitido = Build.VERSION.SDK_INT < Build.VERSION_CODES.TIRAMISU ||
            ContextCompat.checkSelfPermission(
                applicationContext,
                Manifest.permission.POST_NOTIFICATIONS,
            ) == PackageManager.PERMISSION_GRANTED
        if (!permitido) {
            Log.w(
                TAG,
                "notificação descartada: POST_NOTIFICATIONS não concedida " +
                    "(event_type=${data["event_type"]}). O pedido acontece no login.",
            )
            return
        }
        if (!gerente.areNotificationsEnabled()) {
            // Permission granted but notifications switched off in the
            // system settings, or the channel silenced. There is nothing to be
            // done from here either — but it vanishes in silence if nobody
            // logs it.
            Log.w(
                TAG,
                "notificação descartada: notificações desativadas nas configurações " +
                    "(event_type=${data["event_type"]})",
            )
            return
        }

        val notification = ActionableNotificationBuilder.build(applicationContext, data).build()
        val notificationId = ActionableNotificationBuilder.notificationIdFor(
            eventType = data["event_type"].orEmpty(),
            jobId = data["job_id"],
        )
        gerente.notify(notificationId, notification)
    }


    /**
     * Hands a ring event off to [IncomingCallDispatcher]'s registered handler
     * (`:feature-videocall`'s Telecom integration builds the actual native call UI from here —
     * this class never builds a notification for it). A payload missing any required field is
     * logged and dropped rather than partially handled — `internal/videocall/ring.go`'s
     * `announceJoin` always sends all four, so a missing one means either a stale server build
     * or a malformed/spoofed message, neither of which this device should ring for.
     *
     * Reads `caller_name`, not `from`: the FCM SDK reserves the `from` key for the message's
     * sender and strips it out of [RemoteMessage.getData] on this exact device — a server
     * payload keyed `from` would silently never reach here, so `ring.go` sends `caller_name`.
     */
    private fun dispatchIncomingCall(data: Map<String, String>) {
        val roomId = data["room_id"]
        val roomName = data["room_name"]
        val from = data["caller_name"]
        val callId = data["call_id"]
        if (roomId == null || roomName == null || from == null || callId == null) {
            Log.w(TAG, "payload de incoming-call incompleto, ignorado: $data")
            return
        }
        val handler = IncomingCallDispatcher.handler
        if (handler == null) {
            Log.w(TAG, "IncomingCallDispatcher sem handler registrado — chamada de $from ignorada")
            return
        }
        handler.onIncomingCall(roomId = roomId, roomName = roomName, from = from, callId = callId)
    }

    /**
     * Hands a `call-ended` event off to [IncomingCallDispatcher]'s registered handler so a
     * still-ringing (or already-active) call on this device is dismissed the moment the other
     * side hangs up, instead of only clearing once the original ring message's own TTL/
     * collapse-key expires.
     */
    private fun dispatchCallEnded(data: Map<String, String>) {
        val callId = data["call_id"]
        if (callId == null) {
            Log.w(TAG, "payload de call-ended sem call_id, ignorado: $data")
            return
        }
        val handler = IncomingCallDispatcher.handler
        if (handler == null) {
            Log.w(TAG, "IncomingCallDispatcher sem handler registrado — call-ended de $callId ignorado")
            return
        }
        handler.onCallEnded(callId)
    }

    /**
     * Registers this device's current FCM token against the real
     * `POST /api/mobile/v1/notify/devices` endpoint — called from [onNewToken] and, once per
     * process lifetime, right after login (see the post-login onboarding flow in `:app`) so a
     * device that already has a token when the user first logs in still gets registered even if
     * `onNewToken` never fires again for it.
     */
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
        /**
         * PROCESS scope, and not an instance's: the caller of
         * [registrarTokenAvulso] is the app's boot, which has no service in
         * hand. A scope created per call would be discarded before the request
         * finished.
         */
        private val escopoAvulso = CoroutineScope(SupervisorJob() + Dispatchers.IO)

        /**
         * Registers the token when it arrived from outside the service's
         * lifecycle — today, from [FirebaseBootstrap], which asks for the
         * token on the first run.
         *
         * It exists because `onNewToken` only fires when FCM ISSUES a new
         * token: on a device that already had one, it may never fire again,
         * and the device would go for ever without registering with the
         * server. Same path and same repository as [registerToken] — no second
         * way of registering.
         */
        fun registrarTokenAvulso(context: Context, token: String) {
            val app = context.applicationContext
            val deviceId = DeviceIdProvider(app).deviceId()
            escopoAvulso.launch {
                when (val result = PushDeviceRepository().register(deviceId, token)) {
                    is com.vpsmanager.data.push.PushDeviceResult.Success -> Unit
                    is com.vpsmanager.data.push.PushDeviceResult.Error -> Log.w(TAG, result.reason)
                }
            }
        }
    }
}
