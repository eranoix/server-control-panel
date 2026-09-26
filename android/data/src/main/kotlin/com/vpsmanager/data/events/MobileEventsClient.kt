package com.vpsmanager.data.events

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.channels.awaitClose
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.callbackFlow
import kotlinx.coroutines.flow.filter
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock

/**
 * Small seam a screen's ViewModel depends on instead of [MobileEventsClient] directly, so its
 * unit tests can supply a fake `Flow<MobileEvent>` source rather than standing up a real
 * socket — same convention as [com.vpsmanager.data.push.NotifyPreferencesSource]/
 * [com.vpsmanager.data.ops.OpsSource].
 */
interface EventsSubscriber {
    fun subscribe(channel: String): Flow<MobileEvent>
}

/**
 * The public facade every screen's ViewModel calls to get the channels it needs — the only
 * client-visible entry point onto the single [MobileEventsSocket]. Subscriptions
 * are reference-counted per channel string so two callers subscribing to the same channel
 * share one underlying `subscribe` frame: the FIRST subscriber to a channel sends
 * `{"op":"subscribe", ...}`, and only once the LAST subscriber's [Flow] collection is
 * cancelled does `{"op":"unsubscribe", ...}` go out.
 *
 * Resume mechanism: the server's Hub (`internal/mobilebff/events_hub.go`) does
 * not buffer or replay events for a disconnected client — there is no cursor/last-event-id/
 * sequence number to resume from. The only thing that makes reconnect "pick back up" is
 * re-sending a fresh `subscribe` frame per currently-active channel once [MobileEventsSocket]
 * reaches [ConnectionState.CONNECTED] again; this class is what performs that resend
 * (see the `state` collector below), which is also exactly why no duplicate delivery is
 * possible — nothing was ever queued server-side while disconnected.
 */
class MobileEventsClient(
    private val socket: MobileEventsSocket,
    private val scope: CoroutineScope,
) : EventsSubscriber {
    private val mutex = Mutex()
    private val subscriberCounts = mutableMapOf<String, Int>()

    init {
        // Every time the shared socket (re)reaches CONNECTED, resend a subscribe frame for
        // every channel this client currently has active subscribers for. On the very first
        // connect this is a no-op (subscribe() below already sent it); on every reconnect
        // after a drop, this is the entire "resume" story.
        scope.launch {
            socket.state.collect { connectionState ->
                if (connectionState == ConnectionState.CONNECTED) {
                    val activeChannels = mutex.withLock { subscriberCounts.keys.toList() }
                    activeChannels.forEach { channel -> socket.send(ClientOp("subscribe", channel)) }
                }
            }
        }
    }

    /**
     * Returns a [Flow] of every [MobileEvent] delivered on [channel] while collected.
     * Cancelling collection (e.g. a Compose `DisposableEffect` leaving composition, or a
     * ViewModel's `onCleared()`) is this API's unsubscribe signal — there is no separate
     * `unsubscribe(channel)` function to call by hand.
     */
    override fun subscribe(channel: String): Flow<MobileEvent> = callbackFlow {
        mutex.withLock {
            val newCount = (subscriberCounts[channel] ?: 0) + 1
            subscriberCounts[channel] = newCount
            if (newCount == 1) socket.send(ClientOp("subscribe", channel))
        }

        val forwardingJob = scope.launch {
            socket.events.filter { it.channel == channel }.collect { event -> trySend(event) }
        }

        awaitClose {
            forwardingJob.cancel()
            scope.launch {
                mutex.withLock {
                    val remaining = (subscriberCounts[channel] ?: 1) - 1
                    if (remaining <= 0) {
                        subscriberCounts.remove(channel)
                        socket.send(ClientOp("unsubscribe", channel))
                    } else {
                        subscriberCounts[channel] = remaining
                    }
                }
            }
        }
    }
}
