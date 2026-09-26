package pty

import (
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Restart notice for the open terminals.
//
// The problem this solves: when the service restarts (every deploy), the process
// simply dies. Terminal websockets are HIJACKED connections —
// http.Server.Shutdown, by contract, neither closes them nor waits for them. On
// the browser side the connection vanishes with no close frame, which arrives as
// code 1006 ("abnormal closure"), exactly the same symptom as a network drop.
// The practical result: EVERY deploy presented itself to the user as a connection
// problem, and the client only learned of the drop when TCP complained.
//
// With the notice, the client receives 1012 (Service Restart) the instant SIGTERM
// lands: it knows this is expected, knows it lasts ~2s, does not pollute the
// terminal with a failure warning, and comes back quickly.
//
// WriteControl is safe for concurrent use alongside the other write methods (an
// explicit gorilla/websocket guarantee), so we do not need the proxy's write
// mutex here.

var (
	liveMu    sync.Mutex
	liveConns = map[*websocket.Conn]struct{}{}
)

// registerLive records a terminal websocket as live and returns the function that
// removes it. Always called with defer — a leaked registration would hold the
// conn in memory and make the notice write into a dead socket.
func registerLive(c *websocket.Conn) func() {
	if c == nil {
		return func() {}
	}
	liveMu.Lock()
	liveConns[c] = struct{}{}
	liveMu.Unlock()
	return func() {
		liveMu.Lock()
		delete(liveConns, c)
		liveMu.Unlock()
	}
}

// LiveCount returns how many terminals are connected right now. Used in the
// tests and available for diagnostics.
func LiveCount() int {
	liveMu.Lock()
	defer liveMu.Unlock()
	return len(liveConns)
}

// NotifyRestart tells every open terminal that the service is about to restart.
// Called on the SIGTERM path, BEFORE the HTTP server's shutdown.
//
// Best-effort by construction: an already dead socket, a stuck client or a write
// that blows past its deadline must not delay or abort the shutdown — the deploy
// has to happen regardless. Hence the short deadline and the ignored error.
func NotifyRestart() int {
	liveMu.Lock()
	conns := make([]*websocket.Conn, 0, len(liveConns))
	for c := range liveConns {
		conns = append(conns, c)
	}
	liveMu.Unlock()

	msg := websocket.FormatCloseMessage(websocket.CloseServiceRestart, "deploy")
	prazo := time.Now().Add(250 * time.Millisecond)
	for _, c := range conns {
		_ = c.WriteControl(websocket.CloseMessage, msg, prazo)
	}
	return len(conns)
}
