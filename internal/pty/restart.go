package pty

import (
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var (
	liveMu    sync.Mutex
	liveConns = map[*websocket.Conn]struct{}{}
)

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

func LiveCount() int {
	liveMu.Lock()
	defer liveMu.Unlock()
	return len(liveConns)
}

func NotifyRestart() int {
	liveMu.Lock()
	conns := make([]*websocket.Conn, 0, len(liveConns))
	for c := range liveConns {
		conns = append(conns, c)
	}
	liveMu.Unlock()

	msg := websocket.FormatCloseMessage(websocket.CloseServiceRestart, "deploy")
	deadline := time.Now().Add(250 * time.Millisecond)
	for _, c := range conns {
		_ = c.WriteControl(websocket.CloseMessage, msg, deadline)
	}
	return len(conns)
}
