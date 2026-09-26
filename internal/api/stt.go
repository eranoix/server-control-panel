package api

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"server-control-panel/internal/auth"
	"server-control-panel/internal/wsorigin"
)

// STT bridge: client (browser) ↔ vps-manager ↔ WhisperLive (Collabora, Docker).
//
// The client connects authenticated by JWT on /ws/stt/transcribe and keeps
// speaking the protocol it always spoke (Int16 PCM 16kHz mono + control JSON).
// This handler translates that into the WhisperLive protocol (raw Float32 PCM +
// init JSON + segments).
//
// Why this layer rather than a direct browser→WhisperLive connection:
//   1. WhisperLive has no auth — any client can open a connection.
//      The Go layer authenticates by JWT/invite/guest token before the WS is up.
//   2. WhisperLive exposes an idiosyncratic protocol (uid, send_last_n_segments,
//      and so on). The Go layer normalises it into a stable format the frontend knows.
//   3. WhisperLive emits every active segment on each update (not incremental).
//      Here we track each segment by (start,end) so as to emit partial/final
//      only when the text changes or the segment is completed.
//   4. The browser sends Int16 (compact, 2 bytes/sample). WhisperLive wants Float32
//      (4 bytes/sample). Converting on the server saves about 50% of the upload BW.
//   5. WhisperLive has no HTTP health check. /api/stt/health does a local TCP probe.
//
// Audit: stt.session.start/end with duration and lang.
//
// Upstream URL via VPSM_STT_UPSTREAM_URL (default ws://127.0.0.1:9091). Model
// via VPSM_STT_MODEL (default "small" — balanced for CPU; switch to "medium"
// or "large-v3-turbo" for more accuracy at the cost of latency).

const (
	sttPongWait       = 60 * time.Second
	sttPingPeriod     = 25 * time.Second
	sttWriteWait      = 10 * time.Second
	sttMaxMessageSize = 1 << 20 // 1MB — frames PCM Int16 ~2.5KB cada
)

var (
	sttUpstreamDialer = websocket.Dialer{
		HandshakeTimeout: 5 * time.Second,
	}
)

// sttConfig reads the env vars exactly once, at boot.
type sttConfig struct {
	upstreamURL string
	model       string
}

var sttCfg = func() sttConfig {
	upstream := os.Getenv("VPSM_STT_UPSTREAM_URL")
	if upstream == "" {
		upstream = "ws://127.0.0.1:9091"
	}
	model := os.Getenv("VPSM_STT_MODEL")
	if model == "" {
		model = "small"
	}
	return sttConfig{upstreamURL: upstream, model: model}
}()

// clientStartMsg is what the browser sends first, once the WS is open.
type clientStartMsg struct {
	Type   string `json:"type"`
	Lang   string `json:"lang"`
	Prompt string `json:"prompt"`
	Model  string `json:"model"` // opcional; se vazio usa env default
}

// clientControlMsg covers stop and the browser's other textual controls.
type clientControlMsg struct {
	Type string `json:"type"`
}

// wlInitMsg is the JSON WhisperLive expects in the handshake.
type wlInitMsg struct {
	UID                 string  `json:"uid"`
	Language            string  `json:"language"`
	Task                string  `json:"task"`
	Model               string  `json:"model"`
	UseVAD              bool    `json:"use_vad"`
	MaxClients          int     `json:"max_clients"`
	MaxConnectionTime   int     `json:"max_connection_time"`
	SendLastNSegments   int     `json:"send_last_n_segments"`
	NoSpeechThresh      float64 `json:"no_speech_thresh"`
	ClipAudio           bool    `json:"clip_audio"`
	SameOutputThreshold int     `json:"same_output_threshold"`
	InitialPrompt       string  `json:"initial_prompt,omitempty"`
}

// wlSegment mirrors the JSON WhisperLive returns on each update.
type wlSegment struct {
	Start     string `json:"start"` // segundos como string ("1.536")
	End       string `json:"end"`   // segundos como string
	Text      string `json:"text"`
	Completed bool   `json:"completed"`
}

type wlUpdateMsg struct {
	UID      string      `json:"uid"`
	Message  string      `json:"message,omitempty"`
	Backend  string      `json:"backend,omitempty"`
	Segments []wlSegment `json:"segments,omitempty"`
	Status   string      `json:"status,omitempty"`
}

// translateWLControl maps a WhisperLive control message to the payload we send
// to the client. Returns (payload, true) when upd is a control signal we handle
// (SERVER_READY/DISCONNECT); (nil, false) otherwise (WAIT/"" or a data message,
// which follow the normal segment flow).
//
// Extracted from handleSTTTranscribe so it can be tested. The acknowledgement
// of the caption round-trip is anchored on SERVER_READY→{type:"ready"}: a fix
// that removed that mapping would break the activation confirmation in silence.
// TestTranslateWLControl locks the contract.
func translateWLControl(upd wlUpdateMsg) (map[string]any, bool) {
	switch upd.Message {
	case "SERVER_READY":
		return map[string]any{"type": "ready", "backend": upd.Backend}, true
	case "DISCONNECT":
		return map[string]any{"type": "error", "code": "upstream-disconnect", "fatal": true, "message": "backend disconnected"}, true
	}
	return nil, false
}

// segState tracks an active segment identified by its start_time. As long as
// the same start_time keeps appearing, a new end+text counts as a partial
// update of the same segment. When a NEW start_time shows up, the previous
// segment is promoted to final. When the session ends, everything pending becomes final.
type segState struct {
	startMs      int64
	endMs        int64
	text         string
	emittedFinal bool
}

// normalizeLang accepts "pt-BR", "pt-PT", "en-US" and returns "pt", "en" — the
// format faster-whisper consumes.
func normalizeLang(l string) string {
	l = strings.ToLower(strings.TrimSpace(l))
	if l == "" {
		return "pt"
	}
	if idx := strings.IndexAny(l, "-_"); idx > 0 {
		return l[:idx]
	}
	if len(l) > 2 {
		return l[:2]
	}
	return l
}

// handleSTTTranscribe upgrades the client, connects to WhisperLive and bridges
// the two directions, translating both the protocol and the audio format.
//
// Auth accepts 3 kinds of token via ?token= or a cookie (videocall_invite,
// videocall_guest, regular JWT) — guests in a video-call room need to
// transcribe in order to propagate speech over the DC.
func (r *Router) handleSTTTranscribe(w http.ResponseWriter, req *http.Request) {
	user := r.resolveSTTUser(req)
	if user == "" {
		writeErr(w, 401, "unauthorized")
		return
	}

	clientConn, err := wsUpgrader.Upgrade(w, req, wsorigin.SecHeaders())
	if err != nil {
		return
	}
	defer clientConn.Close()
	clientConn.SetReadLimit(sttMaxMessageSize)
	_ = clientConn.SetReadDeadline(time.Now().Add(sttPongWait))
	clientConn.SetPongHandler(func(string) error {
		_ = clientConn.SetReadDeadline(time.Now().Add(sttPongWait))
		return nil
	})

	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()

	clientMu := &sync.Mutex{}

	sendClient := func(v any) error {
		clientMu.Lock()
		defer clientMu.Unlock()
		_ = clientConn.SetWriteDeadline(time.Now().Add(sttWriteWait))
		return clientConn.WriteJSON(v)
	}

	// 1) Read the client's start message (handshake).
	_ = clientConn.SetReadDeadline(time.Now().Add(10 * time.Second))
	mt, data, err := clientConn.ReadMessage()
	if err != nil {
		return
	}
	if mt != websocket.TextMessage {
		_ = sendClient(map[string]any{"type": "error", "code": "bad-handshake", "message": "the first frame must be a JSON start"})
		return
	}
	var start clientStartMsg
	if err := json.Unmarshal(data, &start); err != nil || start.Type != "start" {
		_ = sendClient(map[string]any{"type": "error", "code": "bad-handshake", "message": "expected {type:start, lang}"})
		return
	}
	lang := normalizeLang(start.Lang)
	model := strings.TrimSpace(start.Model)
	if model == "" {
		model = sttCfg.model
	}
	_ = clientConn.SetReadDeadline(time.Now().Add(sttPongWait))

	// 2) Connect to WhisperLive.
	u, _ := url.Parse(sttCfg.upstreamURL)
	upConn, _, err := sttUpstreamDialer.Dial(u.String(), nil)
	if err != nil {
		_ = sendClient(map[string]any{
			"type":    "error",
			"code":    "upstream-unavailable",
			"message": "STT backend offline",
			"fatal":   true,
		})
		return
	}
	defer upConn.Close()
	upConn.SetReadLimit(sttMaxMessageSize)

	// 3) Send the init JSON to WhisperLive.
	// An opaque UID — no user/sub embedded. WhisperLive may log the UID, and
	// today upstream is local (127.0.0.1), but if it is ever remote this keeps
	// metadata from leaking identity.
	var randBytes [8]byte
	_, _ = rand.Read(randBytes[:])
	uid := fmt.Sprintf("vpsm-%x", randBytes[:])
	// Initial prompt: biases the model towards Brazilian-Portuguese
	// conversational context. Reduces hallucination during silence and improves
	// punctuation. Replaceable by start.Prompt if the client sends its own (for
	// instance, specific technical jargon).
	initialPrompt := start.Prompt
	if initialPrompt == "" {
		if strings.HasPrefix(lang, "pt") {
			initialPrompt = "Esta é uma conversa em português brasileiro, com pontuação correta."
		} else if strings.HasPrefix(lang, "en") {
			initialPrompt = "This is a conversation in English, with proper punctuation."
		}
	}
	init := wlInitMsg{
		UID:               uid,
		Language:          lang,
		Task:              "transcribe",
		Model:             model,
		UseVAD:            true,
		MaxClients:        10,
		MaxConnectionTime: 7200,
		// We keep WhisperLive's defaults — I tried aggressive values and they
		// delayed segment emission (the medium model needs more iterations than
		// small to stabilise its output). Aggressive tuning only makes sense with
		// a GPU + large-v3-turbo, where inference is instantaneous.
		SendLastNSegments: 10,
		NoSpeechThresh:    0.45,
		ClipAudio:         false,
		// 5 (the default was 8) — emits the final sooner; with the medium model
		// 5 iterations is enough time to stabilise and still fast for the UX
		// (about 5-8s against 8-16s before). No material effect on accuracy.
		SameOutputThreshold: 5,
		InitialPrompt:       initialPrompt,
	}
	if err := upConn.WriteJSON(init); err != nil {
		_ = sendClient(map[string]any{"type": "error", "code": "upstream-init-failed", "fatal": true})
		return
	}

	r.auditEvent(req, user, "stt.session.start", "lang="+lang+" model="+model)
	startedAt := time.Now()
	defer func() {
		dur := time.Since(startedAt).Round(time.Second).String()
		r.auditEvent(req, user, "stt.session.end", dur)
	}()

	// State for segment dedup. Identified by the start_time string.
	// Promoted to final when a new start_time appears OR when upstream closes.
	segments := make(map[string]*segState) // start -> state
	segOrder := []string{}                 // ordem de chegada pra promover em loop final
	var segMu sync.Mutex

	// queueFinal/queuePartial accumulate messages while the lock is held.
	// The caller calls drainPending() OUTSIDE the lock to emit them all at once —
	// sendClient can block (a slow client on 3G); calling it under the lock would
	// stall the next read from upstream and hold back WhisperLive's backpressure.
	type pendingMsg map[string]any
	queueFinal := func(pending *[]pendingMsg, s *segState) {
		*pending = append(*pending, pendingMsg{
			"type":       "final",
			"text":       s.text,
			"startMs":    s.startMs,
			"endMs":      s.endMs,
			"lang":       lang,
			"confidence": 1.0,
		})
		s.emittedFinal = true
	}
	queuePartial := func(pending *[]pendingMsg, txt string) {
		*pending = append(*pending, pendingMsg{"type": "partial", "text": txt, "lang": lang})
	}
	drainPending := func(pending []pendingMsg) {
		for _, m := range pending {
			_ = sendClient(m)
		}
	}

	var wg sync.WaitGroup

	// Ping pump towards the client.
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(sttPingPeriod)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				clientMu.Lock()
				_ = clientConn.SetWriteDeadline(time.Now().Add(sttWriteWait))
				err := clientConn.WriteMessage(websocket.PingMessage, nil)
				clientMu.Unlock()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()

	// Client → WhisperLive (Int16 → Float32 + control msgs).
	//
	// When the client signals the end (stop or disconnect), we do NOT send
	// END_OF_AUDIO straight away — WhisperLive needs a window of silence to mark
	// the last segment completed:true through `same_output_threshold`. The window:
	//  1. Stop forwarding the client's audio
	//  2. Send chunks of silence (5s) — forces WhisperLive to "resolve" the last
	//     segment and mark it completed
	//  3. Send END_OF_AUDIO to guarantee the flush
	//  4. Wait another 3s to drain the final messages
	//  5. cancel
	const (
		// 5s + 3s drain — I tried shortening it, but the medium model needs the
		// time to process the last window and stabilise the segment.
		sttSilenceDrain = 5 * time.Second
		sttFinalDrain   = 3 * time.Second
		sttSilenceFrame = 1280 * 4 // 80ms of float32 silence (1280 samples * 4 bytes)
	)
	silence := make([]byte, sttSilenceFrame)
	signalEOF := func() {
		// Pump the silence synchronously to give WhisperLive time to complete.
		// Watches ctx so it can abort early if upstream or the client drops during
		// the drain. Tracked in wg so the handler waits before the deferred upConn.Close().
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					// a panic in the drain goroutine must not take the server down.
				}
			}()
			deadline := time.Now().Add(sttSilenceDrain)
			for time.Now().Before(deadline) {
				select {
				case <-ctx.Done():
					return
				default:
				}
				_ = upConn.SetWriteDeadline(time.Now().Add(sttWriteWait))
				if err := upConn.WriteMessage(websocket.BinaryMessage, silence); err != nil {
					break
				}
				time.Sleep(80 * time.Millisecond)
			}
			_ = upConn.SetWriteDeadline(time.Now().Add(sttWriteWait))
			_ = upConn.WriteMessage(websocket.BinaryMessage, []byte("END_OF_AUDIO"))
			time.AfterFunc(sttFinalDrain, cancel)
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			mt, data, err := clientConn.ReadMessage()
			if err != nil {
				signalEOF()
				return
			}
			switch mt {
			case websocket.BinaryMessage:
				// The browser sends Int16 little-endian. WhisperLive wants Float32 LE.
				if len(data)%2 != 0 {
					continue
				}
				out := make([]byte, len(data)*2)
				for i := 0; i < len(data); i += 2 {
					s := int16(binary.LittleEndian.Uint16(data[i:]))
					f := float32(s) / 32768.0
					binary.LittleEndian.PutUint32(out[i*2:], math.Float32bits(f))
				}
				_ = upConn.SetWriteDeadline(time.Now().Add(sttWriteWait))
				if err := upConn.WriteMessage(websocket.BinaryMessage, out); err != nil {
					cancel()
					return
				}
			case websocket.TextMessage:
				var ctrl clientControlMsg
				if err := json.Unmarshal(data, &ctrl); err == nil && ctrl.Type == "stop" {
					signalEOF()
					return
				}
			}
		}
	}()

	// WhisperLive → Client (segments → partial/final translation).
	// When upstream closes, promote any pending partial to final so the last
	// utterance is not lost (faster_whisper does not mark the final segment
	// as completed before END_OF_AUDIO finishes — the final flush has to come
	// from the bridge).
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer cancel()
		defer func() {
			var pending []pendingMsg
			segMu.Lock()
			for _, k := range segOrder {
				if s, ok := segments[k]; ok && !s.emittedFinal && s.text != "" {
					queueFinal(&pending, s)
				}
			}
			segMu.Unlock()
			drainPending(pending)
		}()
		_ = upConn.SetReadDeadline(time.Now().Add(2 * time.Minute))
		for {
			mt, data, err := upConn.ReadMessage()
			if err != nil {
				return
			}
			_ = upConn.SetReadDeadline(time.Now().Add(2 * time.Minute))
			if mt != websocket.TextMessage {
				continue
			}
			var upd wlUpdateMsg
			if err := json.Unmarshal(data, &upd); err != nil {
				continue
			}
			// Control signals. translateWLControl maps SERVER_READY/DISCONNECT
			// to the client's payload — extracted so it can be tested. Armour
			// against accidental deletion: the acknowledgement of the caption
			// round-trip is anchored on SERVER_READY→{type:"ready"}; removing it
			// would break the confirmation in silence. TestTranslateWLControl is the net.
			if payload, handled := translateWLControl(upd); handled {
				_ = sendClient(payload)
				if upd.Message == "DISCONNECT" {
					return
				}
				continue
			}
			// WAIT / "" (and non-control) land here and go on to status/segments.
			if upd.Status == "ERROR" {
				_ = sendClient(map[string]any{"type": "error", "code": "whisper-failed", "fatal": false, "message": "transcription failed"})
				continue
			}

			// Translate segments into partial/final.
			// Each segment has start_time as its identity. WhisperLive re-emits
			// the same segment several times:
			//  - same identity, the text grows → partial update
			//  - same identity, completed:false→true (same text) → final
			//  - a new start_time appeared → promote the earlier unemitted ones
			var pending []pendingMsg
			segMu.Lock()
			seenStarts := make(map[string]bool, len(upd.Segments))
			for _, seg := range upd.Segments {
				txt := strings.TrimSpace(seg.Text)
				if txt == "" {
					continue
				}
				// Bag-of-Hallucinations filter — discards outputs known to be
				// Whisper hallucinations (ICASSP 2025 paper). Cuts about 67% of
				// "Thanks for watching", "[Música]", loops and the like.
				// Applied to BOTH partial and final — do not pollute the UI with junk.
				if hall, reason := isHallucination(txt); hall {
					_ = reason
					seenStarts[seg.Start] = true // mark as seen so the promote logic does not fire
					continue
				}
				k := seg.Start
				seenStarts[k] = true
				existing, ok := segments[k]
				startMs := secStrToMs(seg.Start)
				endMs := secStrToMs(seg.End)
				if !ok {
					st := &segState{startMs: startMs, endMs: endMs, text: txt}
					segments[k] = st
					segOrder = append(segOrder, k)
					if seg.Completed {
						queueFinal(&pending, st)
					} else {
						queuePartial(&pending, txt)
					}
					continue
				}
				if existing.emittedFinal {
					continue
				}
				textChanged := existing.text != txt || existing.endMs != endMs
				if textChanged {
					existing.text = txt
					existing.endMs = endMs
				}
				// Promote to final if WhisperLive marked it completed, even when
				// the text has not changed (the partial→final transition can arrive
				// with no textual change).
				if seg.Completed {
					queueFinal(&pending, existing)
				} else if textChanged {
					queuePartial(&pending, txt)
				}
			}
			// Segments whose start_time is no longer in the batch → WhisperLive
			// has moved on to new segments. Promote the pending ones to final.
			for _, k := range segOrder {
				if seenStarts[k] {
					continue
				}
				if s, ok := segments[k]; ok && !s.emittedFinal {
					queueFinal(&pending, s)
				}
			}
			segMu.Unlock()
			drainPending(pending)
		}
	}()

	wg.Wait()
}

// secStrToMs converts "1.536" → 1536.
func secStrToMs(s string) int64 {
	if s == "" {
		return 0
	}
	// Minimal parsing avoids an extra strconv import. "1.536" → 1.536s → 1536ms.
	var f float64
	if _, err := fmt.Sscanf(s, "%f", &f); err != nil {
		return 0
	}
	return int64(f * 1000)
}

// resolveSTTUser validates the token in 3 ways (regular JWT / invite / guest).
// Returns the user identifier, or "" when it is invalid.
func (r *Router) resolveSTTUser(req *http.Request) string {
	if u := auth.UserFrom(req); u != "" {
		return u
	}
	tok := req.URL.Query().Get("token")
	if tok == "" {
		if c, err := req.Cookie("vpsm_token"); err == nil {
			tok = c.Value
		}
	}
	if tok == "" {
		return ""
	}
	if roomID, _, err := r.auth.VerifyVideocallInviteToken(tok); err == nil {
		return "invite:" + roomID
	}
	if roomID, displayName, err := r.auth.VerifyVideocallGuestToken(tok); err == nil {
		return "guest:" + roomID + ":" + displayName
	}
	if sub, err := r.auth.Parse(tok); err == nil && sub != "" {
		return sub
	}
	return ""
}

// handleSTTHealth probes the WhisperLive backend with a TCP dial (it has no
// HTTP). The frontend uses it to decide whether the whisper-local driver is
// available. Public (read-only, leaks no data).
func (r *Router) handleSTTHealth(w http.ResponseWriter, req *http.Request) {
	u, err := url.Parse(sttCfg.upstreamURL)
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "backend": "whisperlive", "reason": "config-invalid"})
		return
	}
	host := u.Host
	if !strings.Contains(host, ":") {
		host += ":80"
	}
	conn, err := net.DialTimeout("tcp", host, 2*time.Second)
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "backend": "whisperlive", "reason": "offline"})
		return
	}
	_ = conn.Close()
	writeJSON(w, map[string]any{
		"ok":      true,
		"backend": "whisperlive",
		"model":   sttCfg.model,
		"whisper": map[string]any{"model": sttCfg.model, "backend": "faster_whisper"},
	})
}
