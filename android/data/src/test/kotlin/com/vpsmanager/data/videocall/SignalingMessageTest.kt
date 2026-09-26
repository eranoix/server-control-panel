package com.vpsmanager.data.videocall

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Fixtures follow the shape produced by marshaling real `internal/videocall/types.go` values in Go,
 * so they prove the data classes in `SignalingMessage.kt` match the server's actual JSON.
 */
class SignalingMessageTest {

    private val json = Json { ignoreUnknownKeys = true }

    private fun roundTrip(fixture: String): SignalingMessage {
        val decoded = json.decodeFromString(SignalingMessage.serializer(), fixture)
        val reEncoded = json.decodeFromString(SignalingMessage.serializer(), json.encodeToString(SignalingMessage.serializer(), decoded))
        assertEquals("type must survive a decode-then-re-encode round trip", decoded.type, reEncoded.type)
        assertEquals("from must survive a decode-then-re-encode round trip", decoded.from, reEncoded.from)
        assertEquals("room_id must survive a decode-then-re-encode round trip", decoded.roomId, reEncoded.roomId)
        return decoded
    }

    @Test
    fun decodesJoin() {
        val msg = roundTrip("""{"type":"join","room_id":"room123"}""")
        assertEquals("join", msg.type)
        assertEquals("room123", msg.roomId)
    }

    @Test
    fun decodesJoined() {
        val msg = roundTrip(
            """{"type":"joined","payload":{"peer_id":"peerA1","room":{"id":"room123","name":"Meeting room","owner":"sam","members":["sam","guest"],"created_at":1735689600},"peers":[{"id":"peerB2","user":"guest","client_id":"client-uuid-1"}],"turn":{"urls":["turn:vps.example.com:3478?transport=udp"],"username":"1735693200:sam","credential":"aGVsbG8td29ybGQtaG1hYy1iNjQ=","ttl":3600},"politeness_seed":"peerA1"}}""",
        )
        assertEquals("joined", msg.type)
        val payload = requireNotNull(msg.payload).jsonObject
        assertEquals("peerA1", payload["peer_id"]?.jsonPrimitive?.content)
    }

    @Test
    fun decodesPeerJoined() {
        val msg = roundTrip("""{"type":"peer-joined","from":"peerB2","payload":{"id":"peerB2","user":"guest","client_id":"client-uuid-1"}}""")
        assertEquals("peer-joined", msg.type)
        assertEquals("peerB2", msg.from)
        val peer = json.decodeFromJsonElement(PeerInfo.serializer(), requireNotNull(msg.payload))
        assertEquals("peerB2", peer.id)
        assertEquals("guest", peer.user)
        assertEquals("client-uuid-1", peer.clientId)
    }

    @Test
    fun decodesPeerLeft() {
        val msg = roundTrip("""{"type":"peer-left","from":"peerB2"}""")
        assertEquals("peer-left", msg.type)
        assertEquals("peerB2", msg.from)
        assertNull(msg.payload)
    }

    @Test
    fun decodesOffer() {
        val msg = roundTrip("""{"type":"offer","from":"peerA1","to":"peerB2","payload":{"sdp":"v=0...","type":"offer"}}""")
        assertEquals("offer", msg.type)
        assertEquals("peerB2", msg.to)
        val sdp = json.decodeFromJsonElement(SdpPayload.serializer(), requireNotNull(msg.payload))
        assertEquals("offer", sdp.type)
        assertEquals("v=0...", sdp.sdp)
    }

    @Test
    fun decodesAnswer() {
        val msg = roundTrip("""{"type":"answer","from":"peerB2","to":"peerA1","payload":{"sdp":"v=0...","type":"answer"}}""")
        assertEquals("answer", msg.type)
        val sdp = json.decodeFromJsonElement(SdpPayload.serializer(), requireNotNull(msg.payload))
        assertEquals("answer", sdp.type)
    }

    @Test
    fun decodesIce() {
        val msg = roundTrip("""{"type":"ice","from":"peerA1","to":"peerB2","payload":{"candidate":"candidate:1 1 UDP 2122252543 10.0.0.1 54321 typ host","sdpMid":"0","sdpMLineIndex":0}}""")
        assertEquals("ice", msg.type)
        val ice = json.decodeFromJsonElement(IceCandidatePayload.serializer(), requireNotNull(msg.payload))
        assertEquals("0", ice.sdpMid)
        assertEquals(0, ice.sdpMLineIndex)
        assertTrue(ice.candidate.startsWith("candidate:1"))
    }

    @Test
    fun decodesLeave() {
        val msg = roundTrip("""{"type":"leave"}""")
        assertEquals("leave", msg.type)
    }

    @Test
    fun decodesChat() {
        val msg = roundTrip("""{"type":"chat","from":"peerA1","to":"peerB2","payload":"hello"}""")
        assertEquals("chat", msg.type)
        assertEquals(JsonPrimitive("hello"), msg.payload)
    }

    @Test
    fun decodesState() {
        val msg = roundTrip("""{"type":"state","from":"peerA1","payload":{"cam":"off"}}""")
        assertEquals("state", msg.type)
        assertEquals("off", msg.payload?.jsonObject?.get("cam")?.jsonPrimitive?.content)
    }

    @Test
    fun decodesError() {
        val msg = roundTrip("""{"type":"error","error":"room full"}""")
        assertEquals("error", msg.type)
        assertEquals("room full", msg.error)
    }

    @Test
    fun decodesPing() {
        val msg = roundTrip("""{"type":"ping"}""")
        assertEquals("ping", msg.type)
    }

    /**
     * Decodes `credential` (not `password`), `user` (not `display_name`) and `clientId` (used to
     * dedupe ghost tiles) from a bare `JoinResponse`, without the `joined` envelope.
     */
    @Test
    fun decodesJoinResponseWithTurnAndPolitenessSeed() {
        val fixture = """{"peer_id":"peerA1","room":{"id":"room123","name":"Meeting room","owner":"sam","members":["sam","guest"],"created_at":1735689600},"peers":[{"id":"peerB2","user":"guest","client_id":"client-uuid-1"}],"turn":{"urls":["turn:vps.example.com:3478?transport=udp"],"username":"1735693200:sam","credential":"aGVsbG8td29ybGQtaG1hYy1iNjQ=","ttl":3600},"politeness_seed":"peerA1"}"""
        val response = json.decodeFromString(JoinResponse.serializer(), fixture)

        assertEquals("peerA1", response.peerId)
        assertEquals("peerA1", response.politenessSeed)
        assertEquals("room123", response.room.id)
        assertEquals(listOf("sam", "guest"), response.room.members)

        assertEquals(1, response.peers.size)
        val peer = response.peers.first()
        assertEquals("guest", peer.user)
        assertEquals("client-uuid-1", peer.clientId)

        assertTrue("turn.urls must not be empty", response.turn.urls.isNotEmpty())
        assertEquals("aGVsbG8td29ybGQtaG1hYy1iNjQ=", response.turn.credential)
        assertEquals(3600L, response.turn.ttl)
    }

    @Test
    fun decodesTurnCredentialsAlone() {
        val fixture = """{"urls":["turn:vps.example.com:3478?transport=udp"],"username":"1735693200:sam","credential":"aGVsbG8td29ybGQtaG1hYy1iNjQ=","ttl":3600}"""
        val turn = json.decodeFromString(TurnCredentials.serializer(), fixture)
        assertEquals("1735693200:sam", turn.username)
        assertEquals("aGVsbG8td29ybGQtaG1hYy1iNjQ=", turn.credential)
        assertEquals(3600L, turn.ttl)
    }

    @Test
    fun decodesPeerInfoWithoutClientId() {
        val fixture = """{"id":"peerC3","user":"legacy"}"""
        val peer = json.decodeFromString(PeerInfo.serializer(), fixture)
        assertEquals("peerC3", peer.id)
        assertEquals("legacy", peer.user)
        assertNull("clientId must be null when the server omits client_id (omitempty)", peer.clientId)
    }
}
