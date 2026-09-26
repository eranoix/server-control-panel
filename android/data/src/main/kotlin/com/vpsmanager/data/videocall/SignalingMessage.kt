package com.vpsmanager.data.videocall

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonElement

/**
 * Wire-exact mirror of `internal/videocall/types.go`'s `SignalingMsg` — every frame on
 * `/ws/videocall`, in both directions, is exactly this shape. `type` values the server emits:
 * `join`, `joined`, `peer-joined`, `peer-left`, `offer`, `answer`, `ice`, `leave`, `chat`,
 * `state`, `error`, `ping`. [payload] is kept as a raw [JsonElement] (never a fixed sub-schema)
 * because its shape is `type`-specific and, for `offer`/`answer`/`ice`/`chat`, is opaque to the
 * Go server itself — it relays whatever the sending peer (browser or native) put there. Decode
 * further with [SdpPayload]/[IceCandidatePayload]/[JoinResponse]/[PeerInfo] once [type] is known.
 */
@Serializable
data class SignalingMessage(
    val type: String,
    val from: String? = null,
    val to: String? = null,
    @SerialName("room_id") val roomId: String? = null,
    val payload: JsonElement? = null,
    val error: String? = null,
)

/**
 * Wire-exact mirror of `internal/videocall/types.go`'s `Room` — a value type on the wire (never
 * `null`), matching the Go struct's non-pointer `Room` field. `pin`/`pinExpiresAt` are
 * `omitempty` on the Go side (only present while a PIN is active).
 */
@Serializable
data class RoomInfo(
    val id: String,
    val name: String,
    val owner: String,
    val members: List<String>,
    @SerialName("created_at") val createdAt: Long,
    val pin: String? = null,
    @SerialName("pin_expires_at") val pinExpiresAt: Long? = null,
)

/**
 * Wire-exact mirror of `internal/videocall/types.go:52-56`'s `TURNCredentials`. The wire key is
 * `credential` — there is no `password` key, ever. [credential] carries an HMAC-SHA1 base64
 * value; feed it into `org.webrtc.PeerConnection.IceServer.builder(url)
 * .setUsername(username).setPassword(credential).createIceServer()` — `.setPassword(...)` is
 * `org.webrtc`'s own SDK method name, unrelated to this wire key's name. [ttl] mirrors the Go
 * `int64` as `Long`, not `Int`, to avoid silent truncation.
 */
@Serializable
data class TurnCredentials(
    val urls: List<String>,
    val username: String,
    val credential: String,
    val ttl: Long,
)

/**
 * Wire-exact mirror of `internal/videocall/types.go:74-81`'s `PeerInfo`. The wire key is `user`
 * — there is no `display_name` key. [clientId] is `omitempty` on the Go side (nullable here) but
 * is load-bearing when present: per the Go source's own comment, it is the stable client
 * identity the web front end uses to dedup ghost tiles during the reconnect race window, before
 * server-side eviction propagates. The peer-tile rendering must key/dedup remote tiles
 * by [clientId] (falling back to [id] only when [clientId] is null), or the native client will
 * show ghost tiles the web client does not.
 */
@Serializable
data class PeerInfo(
    val id: String,
    val user: String,
    @SerialName("client_id") val clientId: String? = null,
)

/**
 * Wire-exact mirror of `internal/videocall/types.go`'s `JoinResponse` — arrives as the `payload`
 * of a `joined` [SignalingMessage] in reply to a `join`, carrying everything needed to start
 * negotiating without a separate REST round-trip: the caller's own [peerId], the already-present
 * [peers], fresh [turn] credentials, and [politenessSeed] (the caller's own peer id, used as one
 * side of the per-peer-connection lexicographic politeness comparison — see
 * `WebRtcSessionManager.isPolite`).
 */
@Serializable
data class JoinResponse(
    @SerialName("peer_id") val peerId: String,
    val room: RoomInfo,
    val peers: List<PeerInfo>,
    val turn: TurnCredentials,
    @SerialName("politeness_seed") val politenessSeed: String,
)

/**
 * Shape of an `offer`/`answer` [SignalingMessage.payload] — matches
 * `RTCSessionDescription.toJSON()` on the browser side (`internal/webassets/web/vendor/vpsm/
 * videocall.js`'s `jsonRaw(this.pc.localDescription)`) and `org.webrtc.SessionDescription`'s
 * own `type`/`description` pair on the native side (`type` is `"offer"` or `"answer"`). The Go
 * server never types this payload — it is opaque `json.RawMessage` relayed as-is between peers —
 * so this shape is defined by peer interop, not by `internal/videocall/types.go`.
 */
@Serializable
data class SdpPayload(
    val type: String,
    val sdp: String,
)

/**
 * Shape of an `ice` [SignalingMessage.payload] — matches `RTCIceCandidate.toJSON()` on the
 * browser side (`ev.candidate.toJSON()` in `videocall.js`) and the fields
 * `org.webrtc.IceCandidate` needs to reconstruct a candidate on the native side.
 * [sdpMid]/[sdpMLineIndex]/[usernameFragment] are nullable because a trickled end-of-candidates
 * signal (or an older peer) may omit them; the Go server does not validate or require any of
 * these keys, it only relays the object opaquely.
 */
@Serializable
data class IceCandidatePayload(
    val candidate: String,
    val sdpMid: String? = null,
    val sdpMLineIndex: Int? = null,
    val usernameFragment: String? = null,
)
