// handlers_procs.go — HTTP layer for the host process manager.
//
// Wired in NewRouter (api.go). Three endpoints:
//
//	GET  /api/procs?name=&user=&cmd=&min_cpu=&min_mem=&sort=&limit=&offset=&tree=1
//	POST /api/procs/signal       body {pid, signal}
//	GET  /ws/procs               server pushes a fresh list every 2s
//
// Authn is the standard JWT middleware applied to the protected mux in
// NewRouter. Authz: read is open to any authenticated user; signal needs
// either primary OR pid owned by caller's unix username.
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/websocket"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/procs"
)

func (r *Router) handleProcs(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		writeErr(w, 405, "method not allowed")
		return
	}
	if req.URL.Query().Get("tree") == "1" {
		nodes, roots, err := procs.Tree(req.Context())
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, map[string]any{"nodes": nodes, "roots": roots})
		return
	}
	f, by, limit, offset := procsFilterFromQuery(req)
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	infos, total, err := procs.List(req.Context(), f, by, limit, offset)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"procs": infos, "total": total, "limit": limit, "offset": offset})
}

func (r *Router) handleProcsSignal(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	user := auth.UserFrom(req)
	if user == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	var body struct {
		PID    int32  `json:"pid"`
		Signal string `json:"signal"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	if body.PID <= 1 {
		writeErr(w, 400, "invalid pid")
		return
	}
	sig, err := procs.SignalByName(body.Signal)
	if err != nil {
		writeErr(w, 400, "unknown signal: "+body.Signal)
		return
	}
	// Authz: primary can signal anything not on the denylist; non-primary
	// can only signal processes they own. SignalAsOwner does the owner
	// check + start_time re-verification atomically (defeats PID reuse
	// TOCTOU where the kernel recycles the PID between check and kill).
	if r.isPrimary(user) {
		if err := procs.Signal(req.Context(), body.PID, sig); err != nil {
			if err == procs.ErrDenied {
				r.auditEvent(req, user, "procs.signal.denied",
					"pid="+strconv.Itoa(int(body.PID))+" sig="+body.Signal+" reason=safety_list")
				writeErr(w, 403, "pid on safety denylist (init/sshd/self)")
				return
			}
			writeErr(w, 500, err.Error())
			return
		}
	} else {
		if err := procs.SignalAsOwner(req.Context(), body.PID, sig, user); err != nil {
			switch err {
			case procs.ErrDenied:
				r.auditEvent(req, user, "procs.signal.denied",
					"pid="+strconv.Itoa(int(body.PID))+" sig="+body.Signal+" reason=safety_list")
				writeErr(w, 403, "pid on safety denylist (init/sshd/self)")
			case procs.ErrForbidden:
				r.auditEvent(req, user, "procs.signal.denied",
					"pid="+strconv.Itoa(int(body.PID))+" sig="+body.Signal+" reason=not_owner_or_recycled")
				writeErr(w, 403, "forbidden — pid not owned by you or recycled")
			default:
				writeErr(w, 500, err.Error())
			}
			return
		}
	}
	r.auditEvent(req, user, "procs.signal",
		"pid="+strconv.Itoa(int(body.PID))+" sig="+body.Signal)
	writeJSON(w, map[string]string{"status": "ok"})
}

// handleProcsStream pushes a fresh process snapshot every 2s.
// The client sends nothing; we just keep writing. Pong watchdog catches
// dead clients in ≤45s.
func (r *Router) handleProcsStream(w http.ResponseWriter, req *http.Request) {
	conn, err := wsUpgrader.Upgrade(w, req, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	// Same constants as the docker stats stream — keepalive is identical.
	conn.SetReadLimit(1024)
	_ = conn.SetReadDeadline(time.Now().Add(dockerWsPongWait))
	conn.SetPongHandler(func(string) error {
		_ = conn.SetReadDeadline(time.Now().Add(dockerWsPongWait))
		return nil
	})

	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()

	// Reader goroutine: discard frames + relay close.
	go func() {
		for {
			if _, _, e := conn.ReadMessage(); e != nil {
				cancel()
				return
			}
		}
	}()

	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	ping := time.NewTicker(dockerWsPingPeriod)
	defer ping.Stop()

	// Send initial frame immediately so the UI doesn't sit empty for 2s.
	if err := writeProcsFrame(conn, ctx, procsQueryFromURL(req)); err != nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if err := writeProcsFrame(conn, ctx, procsQueryFromURL(req)); err != nil {
				return
			}
		case <-ping.C:
			_ = conn.SetWriteDeadline(time.Now().Add(dockerWsWriteWait))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

type procsQuery struct {
	Filter procs.Filter
	Sort   procs.SortBy
	Limit  int
	Offset int
}

func writeProcsFrame(conn *websocket.Conn, ctx context.Context, q procsQuery) error {
	infos, total, err := procs.List(ctx, q.Filter, q.Sort, q.Limit, q.Offset)
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{
		"procs": infos, "total": total,
		"ts": time.Now().Unix(),
	})
	_ = conn.SetWriteDeadline(time.Now().Add(dockerWsWriteWait))
	return conn.WriteMessage(websocket.TextMessage, payload)
}

func procsFilterFromQuery(req *http.Request) (procs.Filter, procs.SortBy, int, int) {
	q := req.URL.Query()
	f := procs.Filter{
		NameContains: q.Get("name"),
		User:         q.Get("user"),
		CmdContains:  q.Get("cmd"),
	}
	if v, err := strconv.ParseFloat(q.Get("min_cpu"), 64); err == nil {
		f.MinCPU = v
	}
	if v, err := strconv.ParseFloat(q.Get("min_mem"), 64); err == nil {
		f.MinMEM = v
	}
	by := procs.SortBy(q.Get("sort"))
	if by == "" {
		by = procs.SortCPU
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	return f, by, limit, offset
}

func procsQueryFromURL(req *http.Request) procsQuery {
	f, by, limit, offset := procsFilterFromQuery(req)
	if limit <= 0 || limit > 500 {
		limit = 100 // smaller default for WS to keep frames cheap
	}
	return procsQuery{Filter: f, Sort: by, Limit: limit, Offset: offset}
}
