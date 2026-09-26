package com.vpsmanager.data.events

import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonElement

/**
 * Client -> server control frame on `/ws/mobile-events`: `{"op":"subscribe","channel":"x"}` or
 * `{"op":"unsubscribe","channel":"x"}` — same shape, `op` value is the only difference. Matches
 * `internal/mobilebff/events_ws.go`'s `controlFrame` exactly.
 */
@Serializable
data class ClientOp(val op: String, val channel: String)

/**
 * Server -> client ack for a `subscribe`/`unsubscribe` control frame:
 * `{"op":"subscribed","channel":"x"}` / `{"op":"unsubscribed","channel":"x"}`. Matches
 * `internal/mobilebff/events_ws.go`'s `ackFrame` exactly.
 */
@Serializable
data class SubscribedAck(val op: String, val channel: String)

/**
 * Server -> client event frame delivered to every connection subscribed to [channel]:
 * `{"v":1,"channel":"queue.job:abc123","type":"progress","data":{"pct":42}}`. Matches
 * `internal/mobilebff/events_hub.go`'s `Envelope` exactly. [data] is kept as a raw [JsonElement]
 * because its shape is channel/type-specific — only the eventual consumer (a screen's
 * ViewModel) knows how to decode it further.
 */
@Serializable
data class MobileEvent(val v: Int, val channel: String, val type: String, val data: JsonElement)
