package com.vpsmanager.data.videocall

/**
 * Narrow seam `:feature-notifications`' `VpsFirebaseMessagingService` depends on to hand a
 * call-shaped FCM payload off to `:feature-videocall`'s Telecom integration — without either
 * feature module gaining a Gradle dependency on the other (this codebase's feature modules are
 * dependency-free siblings; every cross-feature interaction goes through `:data`, matching the
 * existing precedent of narrow, consumer-defined interfaces such as [VideocallTicketSource]).
 * `:feature-videocall` registers the one production implementation via
 * [IncomingCallDispatcher.handler] at app startup; see that object's own doc for what happens
 * before registration occurs.
 */
interface IncomingCallHandler {
    /** A ring event for [callId] in [roomId]/[roomName] from [from] just arrived via FCM. */
    fun onIncomingCall(roomId: String, roomName: String, from: String, callId: String)

    /**
     * The other side already ended [callId] — sent by `internal/videocall/ring.go`'s
     * `broadcastCallEnded` specifically so a still-ringing device can dismiss its own
     * incoming-call UI instead of waiting out the FCM message's own TTL/collapse-key expiry.
     */
    fun onCallEnded(callId: String)
}

/**
 * Process-wide registration point for the one real [IncomingCallHandler] implementation.
 * A plain nullable var, not a DI graph — matches this codebase's documented current stage (see
 * `VpsManagerApplication`'s own doc: "a real DI graph is introduced in a later phase").
 *
 * Until `:feature-videocall` registers a handler (wired in `VpsManagerApplication.onCreate`,
 * alongside `PhoneAccountRegistrar.ensureRegistered()`), [handler] is `null` and a call-shaped
 * FCM message is silently dropped by the caller (`VpsFirebaseMessagingService` logs a warning
 * instead of crashing) — this can only happen if app wiring itself is broken, not as a normal
 * runtime state, so it is treated as a wiring bug to fix, never a case to design a fallback
 * ring path around.
 */
object IncomingCallDispatcher {
    @Volatile
    var handler: IncomingCallHandler? = null
}
