package dev.servercontrolpanel.data.videocall

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonElement

@Serializable
data class SignalingMessage(
    val type: String,
    val from: String? = null,
    val to: String? = null,
    @SerialName("room_id") val roomId: String? = null,
    val payload: JsonElement? = null,
    val error: String? = null,
)

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

@Serializable
data class TurnCredentials(
    val urls: List<String>,
    val username: String,
    val credential: String,
    val ttl: Long,
)

@Serializable
data class PeerInfo(
    val id: String,
    val user: String,
    @SerialName("client_id") val clientId: String? = null,
)

@Serializable
data class JoinResponse(
    @SerialName("peer_id") val peerId: String,
    val room: RoomInfo,
    val peers: List<PeerInfo>,
    val turn: TurnCredentials,
    @SerialName("politeness_seed") val politenessSeed: String,
)

@Serializable
data class SdpPayload(
    val type: String,
    val sdp: String,
)

@Serializable
data class IceCandidatePayload(
    val candidate: String,
    val sdpMid: String? = null,
    val sdpMLineIndex: Int? = null,
    val usernameFragment: String? = null,
)
