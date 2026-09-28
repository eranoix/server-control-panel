package api

import (
	"context"
	"net/http"
	"server-control-panel/internal/recoveryclaude"
	"time"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/claudever"
	ptysvc "server-control-panel/internal/pty"
)

func (r *Router) handleClaudeVersions(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		writeErr(w, 405, "method not allowed")
		return
	}
	user := auth.UserFrom(req)

	owners := map[int]string{}
	targets := map[int]bool{}
	for _, s := range r.sessReg.List() {
		if s.PID > 0 {
			owners[s.PID] = s.Name
			targets[s.PID] = true
		}
	}

	socks := ptysvc.SessionSockets()

	state := claudever.Detect(func(pid int) string {
		if root := claudever.AncestorIn(pid, targets); root != 0 {
			return owners[root]
		}
		return claudever.AncestorByArgv(pid, socks)
	})

	for _, p := range claudever.DetectExternal("PANEL_RECOVERY=1", "recovery") {
		state.Processes = append(state.Processes, p)
		if !p.Current {
			state.Outdated++
		}
	}

	if !r.isPrimary(user) {
		visible := state.Processes[:0]
		for _, p := range state.Processes {
			if p.Target != "" {
				continue
			}
			if p.Session != "" && r.sessionOwn.VisibleTo(p.Session, user, false) {
				visible = append(visible, p)
			}
		}
		state.Processes = visible
		state.Outdated = 0
		for _, p := range state.Processes {
			if !p.Current {
				state.Outdated++
			}
		}
	}

	writeJSON(w, state)
}

func (r *Router) handleClaudeRecoveryRestart(w http.ResponseWriter, req *http.Request) {
	owner, ok := r.mustPrimary(w, req)
	if !ok {
		return
	}
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), 90*time.Second)
	defer cancel()
	if err := recoveryclaude.Restart(ctx, r.cfg.DataDir); err != nil {
		writeErr(w, 500, "restart recovery container: "+err.Error())
		return
	}
	r.auditEvent(req, owner, "claude.recovery.restart", recoveryclaude.Container)
	writeJSON(w, map[string]any{"status": "ok", "container": recoveryclaude.Container})
}
