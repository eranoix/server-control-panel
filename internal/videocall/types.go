// Package videocall provides WebRTC signaling + room management for the
// vps-manager built-in videoconferencing feature.
//
// Architecture: peer-to-peer WebRTC. The server only relays SDP/ICE between
// peers in the same room and mints time-limited TURN credentials for the
// optional coturn fallback. Media never traverses the server (unless TURN
// relay is needed for symmetric-NAT cases — bounded ~8-10% of networks).
package videocall

import (
	"encoding/json"
)

// Room is the durable identity of a videocall space. Persisted to
// data/videocalls/rooms.json. Members listed here can join unconditionally
// (their JWT carries `sub == owner` or `sub ∈ members`); outsiders need a
// single-use invite token.
type Room struct {
	ID        string   `json:"id"`         // ulid-ish hex (16 bytes)
	Name      string   `json:"name"`       // human label, free-form
	Owner     string   `json:"owner"`      // username (matches JWT sub)
	Members   []string `json:"members"`    // usernames allowed besides owner
	CreatedAt int64    `json:"created_at"` // unix seconds

	// PIN-based anonymous access. When PIN != "", anyone with the PIN
	// can join as a "guest" (kind=videocall_guest JWT, scoped to this
	// room, 4h TTL). The PIN itself is high-entropy (8 alphanumeric,
	// avoiding lookalikes 0/O/1/I/l). PINExpiresAt=0 means never expires
	// (only revoked manually via UI). The owner sees the PIN in plaintext
	// — it's not hashed because we want to display it back; this is a
	// disposable invite credential, not a password.
	PIN          string `json:"pin,omitempty"`
	PINExpiresAt int64  `json:"pin_expires_at,omitempty"`
}

// SignalingMsg is the JSON envelope shared on the /ws/videocall channel.
// Both directions (client→server and server→client) use this shape.
type SignalingMsg struct {
	Type    string          `json:"type"`              // join, joined, peer-joined, peer-left, offer, answer, ice, leave, chat, state, error, ping
	From    string          `json:"from,omitempty"`    // peer id of sender (server-stamped on broadcasts)
	To      string          `json:"to,omitempty"`      // peer id of intended recipient (for offer/answer/ice)
	RoomID  string          `json:"room_id,omitempty"` // only client→server on join
	Payload json.RawMessage `json:"payload,omitempty"` // opaque to server (SDP, ICE candidate, etc.)
	Error   string          `json:"error,omitempty"`   // server→client error message
}

// TURNCredentials are the ephemeral creds returned to a peer on join.
// Format follows the coturn REST API spec (use-auth-secret):
//
//	username = "<expiration_unix_ts>:<user>"
//	password = base64(hmac_sha1(secret, username))
type TURNCredentials struct {
	URLs       []string `json:"urls"`       // turn:host:3478?transport=udp etc.
	Username   string   `json:"username"`   // ts:user
	Credential string   `json:"credential"` // HMAC-SHA1 b64
	TTL        int64    `json:"ttl"`        // seconds until username expires
}

// JoinResponse is the server's reply to a `join` message. Carries the
// assigned peer id, the current peers in the room, and TURN creds.
type JoinResponse struct {
	PeerID string          `json:"peer_id"` // server-assigned id for this connection
	Room   Room            `json:"room"`
	Peers  []PeerInfo      `json:"peers"`
	TURN   TURNCredentials `json:"turn"`
	// PolitenessSeed is used by the client to apply the "perfect negotiation"
	// pattern. The lexicographically larger peer id is "impolite" — it ignores
	// glare offers; the smaller is "polite" — it rolls back. Eliminating glare
	// without a coin flip avoids race-condition bugs in renegotiation.
	PolitenessSeed string `json:"politeness_seed"`
}

// PeerInfo is the public-facing view of a connected peer.
type PeerInfo struct {
	ID   string `json:"id"`
	User string `json:"user"`
	// ClientID is the client's stable identity (a sessionStorage uuid for
	// auth, the jti for a guest). An ADDITIVE field in the JSON — old clients
	// ignore it. The front end uses it to visually dedup phantom tiles in the
	// race window before the server-side eviction propagates. Omitted when empty.
	ClientID string `json:"client_id,omitempty"`
}

// InvitePayload is the body of the magic-link join request. The token is
// a JWT with kind="videocall_invite", consumed single-use via the sessions
// store tombstone.
type InvitePayload struct {
	Token string `json:"token"`
}
