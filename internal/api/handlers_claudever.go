package api

// handlers_claudever.go — which sessions are running an old Claude Code version
//
// The CLI downloads a new version by itself (~1 to 2 per day) and writes
// "✓ Update installed · Restart to update" in the status bar. The notice stays
// there until the session is restarted — and anyone working in sessions that
// last days sees it permanently, without knowing WHICH sessions are behind. In
// practice the notice becomes noise nobody can act on.
//
// Here the lag becomes a verifiable fact (read from /proc/<pid>/exe, not from a
// state file), with the name of the owning session — so the operator restarts
// whatever they want, whenever they want. Nothing is restarted on its own:
// killing a Claude mid-task is worse than the notice.

import (
	"context"
	"net/http"
	"server-control-panel/internal/recoveryclaude"
	"time"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/claudever"
	ptysvc "server-control-panel/internal/pty"
)

func (r *Router) handleClaudeVersoes(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		writeErr(w, 405, "method not allowed")
		return
	}
	user := auth.UserFrom(req)

	// Session-PID → name map, so we can say WHO each Claude belongs to. Without
	// it the operator would get a list of PIDs, which helps nobody decide what
	// to restart.
	donos := map[int]string{}
	alvos := map[int]bool{}
	for _, s := range r.sessReg.List() {
		if s.PID > 0 {
			donos[s.PID] = s.Name
			alvos[s.PID] = true
		}
	}

	// Second anchor: the master's argv carries the path of the session's socket.
	// Without it the dtach backend — the active one — resolves NOTHING: it never
	// records a PID in the registry (the master is forked by `dtach -n`, and the PID
	// the server sees when spawning dies right after), so `alvos` came out empty and
	// every process ended up with no session. The visible effect was the "Reiniciar"
	// button being born disabled on EVERY row: useless by construction, and not
	// because there was no owning session.
	socks := ptysvc.SessionSockets()

	estado := claudever.Levantar(func(pid int) string {
		if raiz := claudever.AncestralEm(pid, alvos); raiz != 0 {
			return donos[raiz]
		}
		return claudever.AncestralPorArgv(pid, socks)
	})

	// The recovery container is brought up SEPARATELY, on purpose: Levantar
	// discards it (mesmoMount) because it has its OWN CLI installation, and comparing
	// against the host's has already called a Claude NEWER than the host "outdated".
	// Here the reference is its own installation, and Alvo tells the front end that this
	// one restarts via the container — not by typing into a pane, which it does not have.
	for _, p := range claudever.LevantarExterno("VPSM_RECOVERY=1", "recovery") {
		estado.Processos = append(estado.Processos, p)
		if !p.Atual {
			estado.Defasados++
		}
	}

	// Only return what the user may see. A non-admin operator has no reason to
	// see processes from sessions that are not theirs.
	if !r.isPrimary(user) {
		visiveis := estado.Processos[:0]
		for _, p := range estado.Processos {
			// The recovery Claude belongs to nobody in particular and restarting it
			// is an admin action — it stays out of the non-primary view.
			if p.Alvo != "" {
				continue
			}
			if p.Sessao != "" && r.sessionOwn.VisibleTo(p.Sessao, user, false) {
				visiveis = append(visiveis, p)
			}
		}
		estado.Processos = visiveis
		estado.Defasados = 0
		for _, p := range estado.Processos {
			if !p.Atual {
				estado.Defasados++
			}
		}
	}

	writeJSON(w, estado)
}

// handleClaudeRecoveryRestart restarts the recovery Claude's container.
//
// It is the action behind the "Reiniciar" button on the /recovery row of the
// version panel. The other rows are restarted by typing into the session's pane —
// this one has no pane (it runs in a container), so it needs its own path.
//
// Admin-only: taking down the emergency tool is not an ordinary user operation.
// Synchronous on purpose — `docker restart` comes back in seconds, and the
// operator needs to know whether it worked before closing the panel.
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
	if err := recoveryclaude.Reinicia(ctx, r.cfg.DataDir); err != nil {
		writeErr(w, 500, "restart recovery container: "+err.Error())
		return
	}
	r.auditEvent(req, owner, "claude.recovery.restart", recoveryclaude.Container)
	writeJSON(w, map[string]any{"status": "ok", "container": recoveryclaude.Container})
}
