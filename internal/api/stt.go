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

const (
	sttPongWait       = 60 * time.Second
	sttPingPeriod     = 25 * time.Second
	sttWriteWait      = 10 * time.Second
	sttMaxMessageSize = 1 << 20
)

var (
	sttUpstreamDialer = websocket.Dialer{
		HandshakeTimeout: 5 * time.Second,
	}
)

type sttConfig struct {
	upstreamURL string
	model       string
}

var sttCfg = func() sttConfig {
	upstream := os.Getenv("PANEL_STT_UPSTREAM_URL")
	if upstream == "" {
		upstream = "ws://127.0.0.1:9091"
	}
	model := os.Getenv("PANEL_STT_MODEL")
	if model == "" {
		model = "small"
	}
	return sttConfig{upstreamURL: upstream, model: model}
}()

type clientStartMsg struct {
	Type   string `json:"type"`
	Lang   string `json:"lang"`
	Prompt string `json:"prompt"`
	Model  string `json:"model"`
}

type clientControlMsg struct {
	Type string `json:"type"`
}

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

type wlSegment struct {
	Start     string `json:"start"`
	End       string `json:"end"`
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

func translateWLControl(upd wlUpdateMsg) (map[string]any, bool) {
	switch upd.Message {
	case "SERVER_READY":
		return map[string]any{"type": "ready", "backend": upd.Backend}, true
	case "DISCONNECT":
		return map[string]any{"type": "error", "code": "upstream-disconnect", "fatal": true, "message": "backend disconnected"}, true
	}
	return nil, false
}

type segState struct {
	startMs      int64
	endMs        int64
	text         string
	emittedFinal bool
}

func normalizeLang(l string) string {
	l = strings.ToLower(strings.TrimSpace(l))
	if l == "" {
		return "en"
	}
	if idx := strings.IndexAny(l, "-_"); idx > 0 {
		return l[:idx]
	}
	if len(l) > 2 {
		return l[:2]
	}
	return l
}

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

	var randBytes [8]byte
	_, _ = rand.Read(randBytes[:])
	uid := fmt.Sprintf("panel-%x", randBytes[:])
	initialPrompt := start.Prompt
	if initialPrompt == "" {
		if strings.HasPrefix(lang, "en") {
			initialPrompt = "This is a conversation in English, with proper punctuation."
		}
	}
	init := wlInitMsg{
		UID:                 uid,
		Language:            lang,
		Task:                "transcribe",
		Model:               model,
		UseVAD:              true,
		MaxClients:          10,
		MaxConnectionTime:   7200,
		SendLastNSegments:   10,
		NoSpeechThresh:      0.45,
		ClipAudio:           false,
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

	segments := make(map[string]*segState)
	segOrder := []string{}
	var segMu sync.Mutex

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

	const (
		sttSilenceDrain = 5 * time.Second
		sttFinalDrain   = 3 * time.Second
		sttSilenceFrame = 1280 * 4
	)
	silence := make([]byte, sttSilenceFrame)
	signalEOF := func() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
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
			if payload, handled := translateWLControl(upd); handled {
				_ = sendClient(payload)
				if upd.Message == "DISCONNECT" {
					return
				}
				continue
			}
			if upd.Status == "ERROR" {
				_ = sendClient(map[string]any{"type": "error", "code": "whisper-failed", "fatal": false, "message": "transcription failed"})
				continue
			}

			var pending []pendingMsg
			segMu.Lock()
			seenStarts := make(map[string]bool, len(upd.Segments))
			for _, secret := range upd.Segments {
				txt := strings.TrimSpace(secret.Text)
				if txt == "" {
					continue
				}
				if hall, reason := isHallucination(txt); hall {
					_ = reason
					seenStarts[secret.Start] = true
					continue
				}
				k := secret.Start
				seenStarts[k] = true
				existing, ok := segments[k]
				startMs := secStrToMs(secret.Start)
				endMs := secStrToMs(secret.End)
				if !ok {
					st := &segState{startMs: startMs, endMs: endMs, text: txt}
					segments[k] = st
					segOrder = append(segOrder, k)
					if secret.Completed {
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
				if secret.Completed {
					queueFinal(&pending, existing)
				} else if textChanged {
					queuePartial(&pending, txt)
				}
			}
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

func secStrToMs(s string) int64 {
	if s == "" {
		return 0
	}
	var f float64
	if _, err := fmt.Sscanf(s, "%f", &f); err != nil {
		return 0
	}
	return int64(f * 1000)
}

func (r *Router) resolveSTTUser(req *http.Request) string {
	if u := auth.UserFrom(req); u != "" {
		return u
	}
	tok := req.URL.Query().Get("token")
	if tok == "" {
		if c, err := req.Cookie("panel_token"); err == nil {
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
