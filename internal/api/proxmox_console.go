package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"server-control-panel/internal/inventory"
	"server-control-panel/internal/pve"
	"server-control-panel/internal/wsorigin"
)

const (
	consolePongWait     = 60 * time.Second
	consolePingPeriod   = 25 * time.Second
	consoleWriteWait    = 10 * time.Second
	consoleMaxClientMsg = 256 << 10
)

type browserMessage struct {
	Type string `json:"type"`
	Data string `json:"data"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

func (r *Router) handleProxmoxConsole(w http.ResponseWriter, req *http.Request) {
	if req.URL.Query().Get("token") != "" {
		writeErr(w, 400, "token does not go in the URL — the panel session travels in the cookie")
		return
	}

	user, ok := r.mustPrimary(w, req)
	if !ok {
		return
	}

	st := r.inventoryStoreOrNil(w)
	if st == nil {
		return
	}
	inv, err := st.Snapshot()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}

	id := strings.TrimSpace(req.URL.Query().Get("node"))
	if id == "" {
		writeErr(w, 400, "node is required (e.g. node=lxc/204)")
		return
	}
	no, found := findNode(inv, id)
	if !found {
		writeErr(w, 404, "node not found: "+id)
		return
	}
	isHost := no.Kind == inventory.NodeKindHost
	if !isHost && (no.Kind != inventory.NodeKindGuest || no.VMID <= 0) {
		writeErr(w, 400, "node "+id+" is neither a guest nor the hypervisor — there is no console for it")
		return
	}

	op := opGuest
	if isHost {
		op = opHypervisorRead
	}
	cli, ok := r.clientForOp(w, op, no)
	if !ok {
		return
	}

	kind, host := kindAndHost(no)
	if isHost {
		host = no.Name
		if host == "" {
			host = hypervisorNameInInventory(inv)
		}
	}

	clientConn, err := wsUpgrader.Upgrade(w, req, wsorigin.SecHeaders())
	if err != nil {
		return
	}
	defer clientConn.Close()
	clientConn.SetReadLimit(consoleMaxClientMsg)
	_ = clientConn.SetReadDeadline(time.Now().Add(consolePongWait))
	clientConn.SetPongHandler(func(string) error {
		return clientConn.SetReadDeadline(time.Now().Add(consolePongWait))
	})

	var writeMu sync.Mutex
	send := func(frameType int, data []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		_ = clientConn.SetWriteDeadline(time.Now().Add(consoleWriteWait))
		return clientConn.WriteMessage(frameType, data)
	}
	sendControl := func(v any) {
		b, err := json.Marshal(v)
		if err != nil {
			return
		}
		_ = send(websocket.TextMessage, b)
	}

	var upConn pve.ConsoleConn
	var upid string
	if isHost {
		upConn, upid, err = cli.ConsoleAttachNode(req.Context(), host)
	} else {
		upConn, upid, err = cli.ConsoleAttach(req.Context(), host, no.VMID, kind)
	}
	if err != nil {
		sendControl(map[string]any{
			"type":    "error",
			"code":    "console-unavailable",
			"fatal":   true,
			"message": "the hypervisor refused the console for " + id + ": " + pveErrorDetail(err),
		})
		r.auditEvent(req, user, "pve.console", "node="+id+" action=opened status=refused reason="+pveErrorDetail(err))
		r.auditEvent(req, user, "pve.console", "node="+id+" action=closed status=refused")
		return
	}
	defer upConn.Close()

	start := time.Now()
	r.auditEvent(req, user, "pve.console", "node="+id+" action=opened upid="+upid)
	defer func() {
		r.auditEvent(req, user, "pve.console",
			fmt.Sprintf("node=%s action=closed upid=%s duration=%s", id, upid, time.Since(start).Round(time.Second)))
	}()

	sendControl(map[string]any{"type": "ready", "node": id, "vmid": no.VMID, "guest": no.Name})

	ready := make(chan struct{})
	var closeOnce sync.Once
	shutdown := func() { closeOnce.Do(func() { close(ready) }) }

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer shutdown()
		for {
			_, data, err := upConn.ReadMessage()
			if err != nil {
				return
			}
			if err := send(websocket.BinaryMessage, data); err != nil {
				return
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer shutdown()
		for {
			_, rawValue, err := clientConn.ReadMessage()
			if err != nil {
				return
			}
			var m browserMessage
			if err := json.Unmarshal(rawValue, &m); err != nil {
				continue
			}
			var frame []byte
			switch m.Type {
			case "input":
				frame = pve.InputFrame([]byte(m.Data))
			case "resize":
				frame = pve.ResizeFrame(m.Cols, m.Rows)
			case "ping":
				frame = pve.KeepaliveFrame()
			default:
				continue
			}
			if frame == nil {
				continue
			}
			_ = upConn.SetWriteDeadline(time.Now().Add(consoleWriteWait))
			if err := upConn.WriteMessage(websocket.BinaryMessage, frame); err != nil {
				return
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(consolePingPeriod)
		defer t.Stop()
		for {
			select {
			case <-ready:
				return
			case <-t.C:
				if err := send(websocket.PingMessage, nil); err != nil {
					shutdown()
					return
				}
				_ = upConn.SetWriteDeadline(time.Now().Add(consoleWriteWait))
				if err := upConn.WriteMessage(websocket.BinaryMessage, pve.KeepaliveFrame()); err != nil {
					shutdown()
					return
				}
			}
		}
	}()

	<-ready
	_ = upConn.Close()
	_ = clientConn.Close()
	wg.Wait()
}
