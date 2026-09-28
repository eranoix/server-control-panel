package dev.servercontrolpanel.data.events

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.channels.awaitClose
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.callbackFlow
import kotlinx.coroutines.flow.filter
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock

interface EventsSubscriber {
    fun subscribe(channel: String): Flow<MobileEvent>
}

class MobileEventsClient(
    private val socket: MobileEventsSocket,
    private val scope: CoroutineScope,
) : EventsSubscriber {
    private val mutex = Mutex()
    private val subscriberCounts = mutableMapOf<String, Int>()

    init {
        scope.launch {
            socket.state.collect { connectionState ->
                if (connectionState == ConnectionState.CONNECTED) {
                    val activeChannels = mutex.withLock { subscriberCounts.keys.toList() }
                    activeChannels.forEach { channel -> socket.send(ClientOp("subscribe", channel)) }
                }
            }
        }
    }

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
