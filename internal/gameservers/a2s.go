package gameservers

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"time"
)

// A2S_INFO query (Steam's query protocol) — this is how you find out how many
// players are online without RCON, which Enshrouded does not have.
//
// Verified against the real server: it answers with a challenge (0x41) and, on
// the second send, returns the 0x49 packet with name, players, slots, type,
// system and password flag.

// A2SInfo is the subset the UI cares about.
type A2SInfo struct {
	Name       string `json:"name"`
	Players    int    `json:"players"`
	MaxPlayers int    `json:"maxPlayers"`
	Version    string `json:"version"`
	Protected  bool   `json:"protected"`
	OK         bool   `json:"ok"`
}

// queryA2S talks to host:port over UDP. The short timeout is deliberate: the
// page's status must not hang if the server never answers.
func queryA2S(host string, port int, timeout time.Duration) (A2SInfo, error) {
	var out A2SInfo
	req := append([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0x54}, []byte("Source Engine Query\x00")...)

	conn, err := net.DialTimeout("udp", fmt.Sprintf("%s:%d", host, port), timeout)
	if err != nil {
		return out, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))

	send := func(p []byte) ([]byte, error) {
		if _, err := conn.Write(p); err != nil {
			return nil, err
		}
		buf := make([]byte, 4096)
		n, err := conn.Read(buf)
		if err != nil {
			return nil, err
		}
		return buf[:n], nil
	}

	resp, err := send(req)
	if err != nil {
		return out, err
	}
	// 0x41 = challenge: resend the request with the 4 bytes appended.
	if len(resp) >= 9 && resp[4] == 0x41 {
		resp, err = send(append(req, resp[5:9]...))
		if err != nil {
			return out, err
		}
	}
	if len(resp) < 6 || resp[4] != 0x49 {
		return out, fmt.Errorf("unexpected A2S response")
	}

	b := resp[6:] // skip header + protocol byte
	readStr := func() string {
		i := bytes.IndexByte(b, 0)
		if i < 0 {
			s := string(b)
			b = nil
			return s
		}
		s := string(b[:i])
		b = b[i+1:]
		return s
	}
	out.Name = readStr()
	_ = readStr() // map (empty on Enshrouded)
	_ = readStr() // folder
	_ = readStr() // game
	// Layout after the 4 strings: id(2) players(1) max(1) bots(1) type(1)
	// environment(1) visibility(1) vac(1) version(string)
	if len(b) < 9 {
		return out, fmt.Errorf("truncated A2S response")
	}
	_ = binary.LittleEndian.Uint16(b[0:2]) // app id
	out.Players = int(b[2])
	out.MaxPlayers = int(b[3])
	out.Protected = b[7] != 0
	b = b[9:]
	out.Version = readStr()
	out.OK = true
	return out, nil
}

// Players queries the server on its query port. An error is NOT fatal: the UI
// simply omits the count, and the rest of the status stays valid.
func (m *Manager) Players(s Server) A2SInfo {
	port := m.queryPort(s)
	if port == 0 {
		return A2SInfo{}
	}
	info, err := queryA2S("127.0.0.1", port, 1500*time.Millisecond)
	if err != nil {
		return A2SInfo{}
	}
	return info
}

// queryPort pulls the query port out of the game config; the adapter is the one
// that knows where that lives.
func (m *Manager) queryPort(s Server) int {
	cfg, err := m.adapter(s).ServerSettings(s)
	if err != nil {
		return 0
	}
	ro, _ := cfg["server"].(map[string]interface{})
	if ro == nil {
		return 0
	}
	if r, ok := ro["_readonly"].(map[string]interface{}); ok {
		if p, ok := r["queryPort"].(float64); ok {
			return int(p)
		}
	}
	return 0
}
