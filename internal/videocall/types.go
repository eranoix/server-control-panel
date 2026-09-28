package videocall

import (
	"encoding/json"
)

type Room struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Owner     string   `json:"owner"`
	Members   []string `json:"members"`
	CreatedAt int64    `json:"created_at"`

	PIN          string `json:"pin,omitempty"`
	PINExpiresAt int64  `json:"pin_expires_at,omitempty"`
}

type SignalingMsg struct {
	Type    string          `json:"type"`
	From    string          `json:"from,omitempty"`
	To      string          `json:"to,omitempty"`
	RoomID  string          `json:"room_id,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Error   string          `json:"error,omitempty"`
}

type TURNCredentials struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username"`
	Credential string   `json:"credential"`
	TTL        int64    `json:"ttl"`
}

type JoinResponse struct {
	PeerID         string          `json:"peer_id"`
	Room           Room            `json:"room"`
	Peers          []PeerInfo      `json:"peers"`
	TURN           TURNCredentials `json:"turn"`
	PolitenessSeed string          `json:"politeness_seed"`
}

type PeerInfo struct {
	ID       string `json:"id"`
	User     string `json:"user"`
	ClientID string `json:"client_id,omitempty"`
}

type InvitePayload struct {
	Token string `json:"token"`
}
