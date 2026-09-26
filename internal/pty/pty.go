package pty

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/docker/docker/api/types/container"
	dockerclient "github.com/docker/docker/client"
	"github.com/gorilla/websocket"

	"server-control-panel/internal/wsorigin"
)

// Keepalive parameters tuned to survive idle proxies/firewalls with 30s
// timeouts AND browsers that throttle background tabs (which can stretch JS
// timers but not protocol-level WS pings). Ping at 25s, expect pong within 45s.
const (
	pongWait   = 45 * time.Second
	pingPeriod = 25 * time.Second
	writeWait  = 10 * time.Second
	maxMessage = 1 << 20 // 1 MiB — paste-buffer headroom
	readChunk  = 8 * 1024
	// maxPauseDuration: ceiling on how long flow control may keep the PTY paused
	// without a resume from the client. Legitimate pauses last seconds (xterm
	// drains MB/s); past that the client is stuck → auto-resume so it never freezes.
	maxPauseDuration = 30 * time.Second
	// maxSessionsPerUser caps how many sessions a single user can create.
	// Each session costs ~10MB (shell + engine overhead). 20 is generous for normal
	// use and blocks a DOS via a curl loop on /ws/shell?name=test$i. Could become
	// configurable through a flag/env later.
	maxSessionsPerUser = 20
	// esperaParaOClienteSair: how long teardown waits for the client (`dtach -a`)
	// to leave on its own before SIGKILL. Two seconds is an eternity for a
	// process that only has to close two descriptors and exit, and short
	// enough that it never holds the handler for a noticeable time.
	esperaParaOClienteSair = 2 * time.Second
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	// Anti-CSRF: reject a WS upgrade whose Origin differs from the Host.
	CheckOrigin: wsorigin.CheckSameHost,
}

type ctrlMsg struct {
	Type string `json:"type"`
	Cols uint16 `json:"cols,omitempty"`
	Rows uint16 `json:"rows,omitempty"`
	Data string `json:"data,omitempty"`
	// X: the SESSION column where this client's crop starts, for the "pan"
	// type. Only meaningful in frame mode — see `quadro.go`. Without it, on a
	// client narrower than the session the right half would be unreachable.
	X int `json:"x,omitempty"`
}

// ownedSessionCount returns how many sessions the registry has recorded for the
// user. It does not filter "alive vs dead" — the listing auto-purges on the next
// operation, and the registry is refreshed then.

// HostShell spawns (or reattaches to) a session and pipes it through
// the websocket. Session names are taken verbatim from the client (just
// sanitised to [A-Za-z0-9_-], max 40 chars) — no "vpsm-<user>-" prefix
// glued on. Ownership is recorded in the *Ownership registry instead.
//
// `user` must come from the JWT (never the query string) so an attacker
// cannot hijack another user's session. The session name comes from
// ?name= (preferred) or ?tab= (legacy alias).
//
// primary should be true for any system admin (config.Primary or a user
// flagged Admin — see config.IsAdmin); when true, attaching to an unowned
// existing session claims it. When false, attaching to an unowned existing
// session is rejected (the session belongs to the admins' adoption pool).
//
// If dtach is not installed we fall back to a plain login shell.
// claudeConfigDir is the CLAUDE_CONFIG_DIR for the "terminal"
// consumer's assigned account, or "" to inherit the default ($HOME/.claude).
// Injected via the backend's env seam so any `claude` run inside the pane uses
// the chosen account. NOTE: `new-session -A` only applies -e when CREATING the
// session — switching accounts requires killing + recreating the pane (the env
// is frozen at first attach). The UI surfaces this.
// primingDoServidor decides, for one connection, which of the TWO mechanisms the
// server has to "fill the client's screen" should run: re-emitting the
// recorded history (`attachReplay`) and forcing the remote program to repaint
// (`repaint-wobble`).
//
// They are two mechanisms and a single question: WHO REBUILDS THE SCREEN. They lived
// behind separate flags for far too long, and it was that separation that left the
// Android app — which rebuilds its own screen — getting a wobble on
// every attach. See the evidence block on `repaint-wobble`, in [HostShell].
//
//	attach=1  → "I already have the screen" (reconnect): no history, no repaint.
//	replay=0  → "I rebuild the screen": no history, no repaint.
//	none      → client that does not prime itself (the web panel): both.
func primingDoServidor(attach, replay string) (mandarHistorico, forcarRepaint bool) {
	attachFresco := attach != "1"
	clienteReconstroiATela := replay == "0"
	// HISTORY and REPAINT are DIFFERENT questions again, and this time the
	// separation is the right answer, not the defect.
	//
	// A client that rebuilds the screen on its own (the app) does not want the
	// server's history — it would show everything twice. But it DOES need the
	// repaint, and the reason is measured in the comment on `wobble`: the log is a
	// cut of a live stream taken at an arbitrary instant, and in the middle of a
	// frame it rebuilds a half-painted screen. Only the program knows how to draw
	// the whole frame.
	//
	// On a reconnect (attach=1) neither runs: the client's in-memory grid is
	// intact, and repainting over it would duplicate.
	return attachFresco && !clienteReconstroiATela, attachFresco
}

func HostShell(w http.ResponseWriter, r *http.Request, user string, primary bool, own *Ownership, claudeConfigDir string, dataDir string, reg *Registry) {
	conn, err := upgrader.Upgrade(w, r, wsorigin.SecHeaders())
	if err != nil {
		return
	}
	defer conn.Close()

	// The session engine is `dtach`, and it is the only one. There was a flag
	// selector (VPSM_SESSION_BACKEND) while two engines ran side by side, for an
	// instant rollback during the migration; the migration finished and the selector
	// went away. `NewSessionBackend` survives anyway because it keeps callers
	// decoupled from the concrete engine — which cost nothing and pays off the day
	// there is a second one.
	//
	// THE SWEEP WENT ALL THE WAY: the old engine's name is gone from the whole
	// package. If someone reintroduces it in a comment, that is a sign they copied
	// stale text — the engine is `dtach`, and it is the only one.
	//
	// What the sweep did NOT erase is the REASONING behind the decisions: wherever
	// the difference between the previous engine and this one explained a choice,
	// the explanation stayed, described by BEHAVIOUR instead of by name. A name is a
	// label; what makes someone understand why the code looks like this is the
	// behaviour.
	backend := NewSessionBackend(dataDir, reg)

	user = safeSessionName(user)
	if user == "" {
		user = "anon"
	}
	// Both ?name= (preferred) and ?tab= (legacy alias) accept the raw
	// session name. Sanitise but do NOT glue any prefix on.
	sessionName := safeSessionName(r.URL.Query().Get("name"))
	if sessionName == "" {
		sessionName = safeSessionName(r.URL.Query().Get("tab"))
	}
	if sessionName == "" {
		sessionName = "main"
	}

	// attach-only (auto-reconnect): NEVER resurrect a session that has ended.
	// The web-terminal pane keeps a reconnect loop; killing a session while its
	// pane is still open used to race that reconnect, which recreated the
	// session via `new-session -A` (attach-OR-create) below — so a deliberately
	// deleted session reappeared "active" seconds later. On every reconnect the
	// client sets ?attach=1; if the session is gone we refuse with a distinct
	// close code (4404) so the pane stops reconnecting and shows a "session
	// ended" notice instead of spawning a blank shell in its place.
	//
	// SECURITY: the precise liveness signal (4404) only reaches whoever MAY touch the
	// name — the owner, the "*" audience or an admin. A non-owner gets the
	// SAME generic "session not found" the ACL would give for someone else's live
	// session; otherwise attach=1 becomes an existence oracle (telling
	// "dead/nonexistent" from "alive-but-another's" breaks the 404-not-403 discipline).
	if r.URL.Query().Get("attach") == "1" {
		owner := own.Owner(sessionName)
		if owner == user || owner == AudienceAll || primary {
			if alive, _ := backend.Has(sessionName); !alive {
				_ = conn.WriteControl(websocket.CloseMessage,
					websocket.FormatCloseMessage(4404, "session ended"),
					time.Now().Add(writeWait))
				return
			}
		} else {
			conn.WriteMessage(websocket.TextMessage, []byte("session not found"))
			return
		}
	}

	// ACL check (mirrors handleTerminalKillSession's 404). The branch
	// where the registry says nobody owns the name is the interesting
	// one: if the session already exists on the host and user is
	// NOT primary, we refuse (it's part of the primary's adoption pool);
	// if it doesn't exist OR user IS primary, we claim it now and let
	// `new-session -A` either attach or create.
	owner := own.Owner(sessionName)
	switch {
	case owner == user || owner == AudienceAll:
		// ours, or shared to everyone ("Todos") — proceed. We do NOT
		// re-Claim here: that would overwrite the audience and steal posse.
	case owner != "" && owner != user:
		// owned by another profile: an admin (master session manager) may
		// attach it — proceed WITHOUT re-Claim so posse/audience stay intact.
		// A non-admin never even learns it exists (404, not 403).
		if !primary {
			conn.WriteMessage(websocket.TextMessage, []byte("session not found"))
			return
		}
	default: // owner == ""
		exists, _ := backend.Has(sessionName)
		if exists && !primary {
			// legacy unowned session — only primary can adopt.
			conn.WriteMessage(websocket.TextMessage, []byte("session not found"))
			return
		}
		// Before creating a NEW session (one that does not exist yet), apply the
		// per-user quota. Re-attaching to your own existing session does not
		// count — only creation. Blocks a DOS by an endless session-creating loop.
		if !exists {
			current := ownedSessionCount(own, user)
			if current >= maxSessionsPerUser {
				conn.WriteMessage(websocket.TextMessage,
					[]byte("session limit reached (cap "+strconv.Itoa(maxSessionsPerUser)+") — close one first"))
				return
			}
		}
		if err := own.Claim(sessionName, user); err != nil {
			// concurrent claim wedged us out.
			conn.WriteMessage(websocket.TextMessage, []byte("session not found"))
			return
		}
	}

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}

	// Session env injected at CREATION time: CLAUDE_CONFIG_DIR. The backend
	// maps it — dtachBackend via
	// `systemd-run --setenv`. It is exactly what the legacy path injected, and the
	// behaviour is identical. backend.Attach returns the
	// client (attach-or-create), confined to user.slice when it creates the server.
	var sessionEnv []string
	if claudeConfigDir != "" {
		sessionEnv = append(sessionEnv, "CLAUDE_CONFIG_DIR="+claudeConfigDir)
	}
	cmd, aerr := backend.Attach(sessionName, []string{shell, "-l"}, sessionEnv, "")
	if aerr != nil || cmd == nil {
		cmd = exec.Command(shell, "-l")
	}
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	// also carry CLAUDE_CONFIG_DIR on the client env — harmless for
	// the session client, and essential for the no-dtach fallback shell above.
	if claudeConfigDir != "" {
		cmd.Env = append(cmd.Env, "CLAUDE_CONFIG_DIR="+claudeConfigDir)
	}
	// Per-pane AI provider — ?ai=oauth|proxy|uncensored|venice. Empty/oauth
	// leaves env untouched (router toggle applies). Other values override
	// ANTHROPIC_BASE_URL + ANTHROPIC_API_KEY to point at a specific upstream
	// directly, bypassing the router. See internal/pty/aienv.go.
	if extraEnv := loadAIEnv(user, safeAIProvider(r.URL.Query().Get("ai"))); len(extraEnv) > 0 {
		cmd.Env = append(cmd.Env, extraEnv...)
	}
	ptmx, err := pty.Start(cmd)
	if err != nil {
		conn.WriteMessage(websocket.TextMessage, []byte("failed to start pty: "+err.Error()))
		return
	}
	defer func() {
		// Kills only the CLIENT process (cmd) — the `dtach -a`. SIGHUP merely
		// DETACHES; the session (the dtach master) stays
		// alive in user.slice. A reattach picks up where it left off.
		//
		// ── AND THE WAIT HAS A DEADLINE ──────────────────────────────────────
		//
		// This used to be a bare `cmd.Process.Wait()`. A `Wait` with no deadline inside
		// a handler is a hang waiting to happen: all it takes is the client not dying
		// on SIGHUP — pty already closed, signal ignored, process stuck in I/O —
		// and `HostShell` NEVER RETURNS.
		//
		// The cost of that stopped being "one leaked process" and became
		// active damage, because of two things this file now does:
		//
		//  1. The connection still counts as attached, so it is still the SCRIBE
		//     for the session log (`sessionlog_compartilhado.go`) — a dead
		//     connection doing the recording.
		//  2. Its size still counts towards the SMALLEST
		//     (`tamanho_da_sessao.go`), and a dead connection with a small
		//     window SHRINKS EVERYONE'S SESSION, forever. There is no operator
		//     gesture that undoes it; only restarting the server.
		//
		// So: ask politely, wait a little, and kill. A leaked process is a
		// nuisance; a handler that never returns is a leak that gets worse over
		// time. If even SIGKILL does not settle it (process in D state, pinned to
		// stuck disk I/O), we give up and RETURN anyway — releasing the session
		// slot matters more than reaping the child.
		_ = ptmx.Close()
		if cmd.Process == nil {
			return
		}
		_ = cmd.Process.Signal(syscall.SIGHUP)
		encerrou := make(chan struct{})
		go func() {
			_, _ = cmd.Process.Wait()
			close(encerrou)
		}()
		select {
		case <-encerrou:
			return
		case <-time.After(esperaParaOClienteSair):
		}
		log.Printf("[pty] client did not exit on SIGHUP, killing it (session %q)", sessionName)
		_ = cmd.Process.Kill()
		select {
		case <-encerrou:
		case <-time.After(esperaParaOClienteSair):
			log.Printf("[pty] client survived SIGKILL (session %q) — releasing the slot anyway", sessionName)
		}
	}()

	// Tee the pty output into the session log (replaces capture-pane; see
	// sessionlog.go). Absolutely best-effort: openSessionLog never returns nil and
	// Write swallows errors — the terminal must never break because of the log. It
	// is wired in from the very start so the log is already populated when read.
	//
	// ONE writer per SESSION, shared by every connection — see
	// [pegarLogDaSessao]. Opening one per connection wrote every byte once per
	// attached client, and the Android app, which rebuilds the screen by replaying
	// this log, got the same frame twice.
	var tee io.Writer
	var sessaoCompartilhada *logCompartilhado
	var idDaConexao int64
	if dataDir != "" {
		slog, compartilhado, id, soltar := pegarLogDaSessao(dataDir, user, sessionName)
		defer soltar()
		tee = slog
		sessaoCompartilhada = compartilhado
		idDaConexao = id
		// The log must not stop when this tab closes: the session stays alive
		// producing output, and without a recorder of its own none of that is
		// written anywhere. See `gravador.go` — idempotent, best-effort, and it
		// never creates a session.
		GaranteGravador(dataDir, user, sessionName, reg)
	}

	// ── Scrollback priming on a FRESH attach (dtach keeps no screen) ──────────
	// On a new attach (not a reconnect) the xterm starts empty and dtach re-emits
	// nothing. For a plain SHELL we replay the tee-log history (the client "lands"
	// straight into its scrollback instead of a black screen). TUI sessions
	// (claude/vim) are skipped — there the repaint comes from the wobble below. A
	// reconnect (attach=1) does NOT replay: the xterm still holds the content, it
	// would duplicate. Absolutely best-effort.
	//
	// `replay=0` is the client saying "I AM THE ONE WHO REBUILDS THE SCREEN" — and
	// that waives BOTH mechanisms here: this block and the repaint-wobble below.
	// See [clienteReconstroiATela] for why tying the two together is right, and
	// why splitting them cost the operator three corrupted screens.
	mandarHistorico, forcarRepaint := primingDoServidor(
		r.URL.Query().Get("attach"),
		r.URL.Query().Get("replay"),
	)
	// The same "I rebuild the screen" that decides the history also decides whether
	// `dtach`'s attach-time clear is allowed to reach this client — see
	// [aparadorDaLimpezaDeAttach].
	clientePrimaAPropriaTela := r.URL.Query().Get("replay") == "0"

	if dataDir != "" && mandarHistorico {
		if rep := attachReplay(dataDir, user, sessionName); len(rep) > 0 {
			_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
			_ = conn.WriteMessage(websocket.BinaryMessage, rep)
		}
	}

	// ── Force-repaint on attach (terminal "black/frozen" on reattach) ─────────
	// dtach keeps no screen; it attaches with `-r winch` (a single SIGWINCH). TUI
	// apps whose renderer only emits on the DIFF (Ink/Claude Code) re-render into the
	// SAME buffer when the size does not change → ZERO bytes written → the new client
	// stays BLACK. The only cure used to be opening the session in another client
	// (VSCode) at a DIFFERENT size, which forces a full relayout+repaint. We reproduce
	// that ourselves: on attach we "wobble" by one row (R-1 → R) with enough slack for
	// the app to paint the intermediate frame — so the repaint happens even when
	// reattaching at the same size. Once per connection (sync.Once); it always
	// restores the MOST recent size the client reported, so it never fights a
	// concurrent resize.
	var (
		szMu               sync.Mutex
		lastCols, lastRows uint16
		avisarCliente      func(uint16, uint16)
		escreverNoCliente  func([]byte) error
		repaintOnce        sync.Once
	)

	// ── FRAME MODE: WHAT THIS CLIENT SEES, COMPOSED FOR IT ───────────────
	//
	// See `quadro.go`. In short: when this client's window is SMALLER than the
	// session, it stops receiving the raw stream (which is drawn for the session's
	// grid and would land entirely in the wrong place) and starts receiving a
	// rendered crop of the server's screen, diffed line by line.
	//
	// This is what lets the session sit at the LARGEST client instead of the
	// smallest — that is, the phone stops shrinking the desktop.
	aceitaQuadro := r.URL.Query().Get("quadro") == "1"
	var (
		quadroMu               sync.Mutex
		quadro                 *quadroDoCliente
		janelaCols, janelaRows uint16 // a janela REAL deste cliente
		rolouAcumulado         int
		pararQuadro            func()
	)
	sinalDeQuadro := make(chan struct{}, 1)
	emModoQuadro := func() bool {
		quadroMu.Lock()
		defer quadroMu.Unlock()
		return quadro != nil
	}

	// ── THE SIZE BELONGS TO THE SESSION, AND IT RULES EVERY CLIENT ───────
	//
	// aplicaTamanho puts the session's EFFECTIVE size on THIS connection, and does
	// both halves together because they are a single decision:
	//
	//  1. the pty of THIS connection — it is what this `dtach -a` reports to the
	//     master. Without it, every connection kept a different size and the master
	//     arbitrated between clients that disagreed. Worse: an arriving client was
	//     sized by nobody, because `pty.Start` creates the pty at 0x0 and the only
	//     path that fixed it was the repaint-wobble — by accident.
	//
	//  2. the client's grid (the `{"type":"size"}` notice), for whoever asked for
	//     `size=1`. A client drawing a grid different from the PTY's puts every
	//     piece of text in the wrong place.
	//
	// Reapplying the same size is cheap and safe: the kernel compares the winsize
	// before signalling (`tty_do_resize`), so an identical TIOCSWINSZ does NOT
	// raise SIGWINCH and does not make the program repaint.
	aplicaTamanho := func(cols, rows uint16) {
		if cols < 2 || rows < 1 {
			return
		}
		szMu.Lock()
		lastCols, lastRows = cols, rows
		avisar := avisarCliente
		szMu.Unlock()
		// The pty of THIS connection goes to the session's size even in frame
		// mode: it is what this `dtach -a` reports to the master, and that is what
		// stops this client from dragging the session down to its own size.
		_ = pty.Setsize(ptmx, &pty.Winsize{Cols: cols, Rows: rows})

		quadroMu.Lock()
		jc, jr := janelaCols, janelaRows
		menorQueASessao := aceitaQuadro && jc >= 2 && jr >= 1 && (jc < cols || jr < rows)
		if menorQueASessao {
			if quadro == nil {
				quadro = novoQuadroDoCliente(int(jc), int(jr))
			} else {
				quadro.redimensiona(int(jc), int(jr))
			}
		} else if quadro != nil {
			// The session now fits in its window: back to the raw stream, which is
			// cheaper and is the usual path. The program will repaint.
			quadro = nil
		}
		emQuadro := quadro != nil
		quadroMu.Unlock()

		if emQuadro {
			// In frame mode it draws ITS OWN grid — being told "draw
			// 120x40" in a 53x45 window is exactly the defect frame mode
			// exists to prevent.
			//
			// And the notice carries ITS window rather than being suppressed: a
			// client that had already been told the session's grid before it
			// shrank would stay stuck on it (the panel remembers the last notice
			// and the FitAddon obeys — see `_gradeSessao` in 00-shell.js). Sending
			// its own window is how you say "go back to drawing your own size"
			// using the mechanism that already exists, instead of inventing another.
			if avisar != nil {
				avisar(jc, jr)
			}
			select {
			case sinalDeQuadro <- struct{}{}:
			default:
			}
			return
		}
		if avisar != nil {
			avisar(cols, rows)
		}
	}
	// ── THE FRAME PUMP ───────────────────────────────────────────────────
	//
	// It lives in a goroutine of its own on purpose: what announces that the screen
	// changed is the RECORDER, and the recorder is the only thing feeding the
	// session log. Composing and writing to the websocket in there would let one
	// slow client hold up the record of the entire session. The notice only raises
	// a flag; the work happens here.
	if aceitaQuadro && dataDir != "" {
		tela := telaDe(dataDir, user, sessionName)
		if tela != nil {
			cancelaAssinatura := tela.assina(func(rolou int) {
				quadroMu.Lock()
				rolouAcumulado += rolou
				quadroMu.Unlock()
				select {
				case sinalDeQuadro <- struct{}{}:
				default: // a signal is already pending: the next frame covers it
				}
			})
			fimDoQuadro := make(chan struct{})
			pararQuadro = func() {
				cancelaAssinatura()
				close(fimDoQuadro)
			}
			go func() {
				defer func() {
					if r := recover(); r != nil {
						log.Printf("[pty] frame pump of session %q died: %v", sessionName, r)
					}
				}()
				for {
					select {
					case <-fimDoQuadro:
						return
					case <-sinalDeQuadro:
					}
					// Coalesce whatever arrives over the next few milliseconds:
					// fast typing does not need one frame per keystroke.
					time.Sleep(16 * time.Millisecond)

					quadroMu.Lock()
					q := quadro
					rolou := rolouAcumulado
					rolouAcumulado = 0
					quadroMu.Unlock()
					if q == nil {
						continue
					}
					szMu.Lock()
					escreve := escreverNoCliente
					szMu.Unlock()
					if escreve == nil {
						continue
					}
					var saida []byte
					if b := q.rolou(rolou); len(b) > 0 {
						saida = append(saida, b...)
					}
					grade, cur, visivel := tela.telaECursor()
					if b := q.atualiza(grade, cur, visivel); len(b) > 0 {
						saida = append(saida, b...)
					}
					if len(saida) > 0 {
						if err := escreve(saida); err != nil {
							return
						}
					}
				}
			}()
		}
	}

	// Registered RIGHT AWAY, before the first resize: that way a connection that
	// arrives mid-session gets the size in force without having had to speak.
	if sessaoCompartilhada != nil {
		if c, r := sessaoCompartilhada.registraAplicador(idDaConexao, aplicaTamanho); c > 0 && r > 0 {
			aplicaTamanho(c, r)
		}
	}
	wobble := func() {
		// Let any BURST of reattach output settle before forcing the repaint —
		// otherwise the app repaints and the burst immediately dirties the screen again
		// (typical on deploy: the server restarts, the session reattaches, and Claude
		// Code dumps its current frame into the new client).
		time.Sleep(180 * time.Millisecond)
		szMu.Lock()
		c, rw := lastCols, lastRows
		szMu.Unlock()
		if c == 0 || rw == 0 { // client has not sent a size yet → use the master's
			if ws, err := pty.GetsizeFull(ptmx); err == nil {
				c, rw = ws.Cols, ws.Rows
			}
		}
		if c == 0 || rw < 2 {
			return
		}
		// A DRAMATIC shrink, but **in ROWS only**: TUI apps (Ink/Claude
		// Code) IGNORE or coalesce small nudges and only invalidate+repaint the
		// whole screen on a BIG change — which is exactly what minimising the
		// window used to unstick. Shrink → hold (the app re-lays out) → restore
		// (invalidate again → full repaint, clearing the reattach
		// corruption). Typical on deploy: the server restarts, the session
		// reattaches at the same size and the app's partial frame dirties the screen.
		//
		// ── WHY COLUMNS ARE NO LONGER PART OF THIS ──────────────────────────
		// This wobble used to touch `Cols` as well (it was `Cols: c / 2`), and that
		// half was the root cause of two defects the app's owner reported as if
		// they were separate — "the text is squeezed in the history" and "the
		// history duplicates". The proof is in the service log itself:
		//
		//   Sep 06 18:52:37 [pty] repaint on attach (grow and back): 49x37 → 24x18 → 49x40 (session "Aplicativo")
		//
		// 24 columns is exactly the width of the squeezed text in the screenshot
		// he sent, and 18:52 is the timestamp shown in it.
		//
		// Width is CONTENT, not screen geometry. Dropping to 24 columns made Claude
		// Code re-render the ENTIRE conversation wrapped at 24 columns; going back
		// to 49 re-rendered all of it again at 49. In a renderer that repaints by
		// walking the cursor up (Ink), a frame taller than the screen cannot erase
		// itself — the `ESC[nA` saturates at the first line of the SCREEN and never
		// reaches the scrollback. Both versions STAY, one below the other: that is
		// the duplication, carrying the same timestamp on both.
		//
		// And the damage did not stop at the screen: all of it is teed into the
		// session log (`sessionlog.go`), so every attach RECORDED a 24-column block
		// that every future attach re-emitted on replay. The "Aplicativo" log held
		// 68 stretches of width 24 and 49 of width 23 — sediment from old wobbles,
		// which no terminal can reflow afterwards (the break is the program's
		// own `\r\n`, not a terminal wrap).
		//
		// Rows have no such effect: changing `Rows` sends the same SIGWINCH and
		// forces the same re-layout, but does NOT change where text wraps. Nothing
		// squeezed is produced, nothing squeezed is recorded.
		// ── AND IT GROWS, NEVER SHRINKS ──────────────────────────────────
		//
		// It was `rw / 2`. Shrinking is what did the damage: taking rows away MAKES
		// THE SCREEN SCROLL — the content at the bottom leaves the active area and
		// goes into the scrollback — and giving them back brings nothing back. The
		// remote program then painted a 24-row frame and a 48-row one on top of it,
		// and the two fused. Three corrupted screens reported by the owner came from that.
		//
		// Growing by one row sends the SAME SIGWINCH and forces the SAME re-layout,
		// and costs nothing: the new rows appear blank at the bottom, nothing
		// scrolls, nothing is truncated, and since rows do not change where text
		// wraps, nothing is reflowed. The intermediate frame is the same frame one
		// row taller, and the final repaint covers all of it.
		maior := pty.Winsize{Cols: c, Rows: rw + 1}
		_ = pty.Setsize(ptmx, &maior)
		time.Sleep(350 * time.Millisecond)
		szMu.Lock() // restore to the most recent size
		c2, r2 := lastCols, lastRows
		szMu.Unlock()
		if c2 == 0 || r2 == 0 {
			c2, r2 = c, rw
		}
		_ = pty.Setsize(ptmx, &pty.Winsize{Cols: c2, Rows: r2})
		log.Printf("[pty] repaint wobble on attach: %dx%d → %dx%d → %dx%d (session %q)", c, rw, maior.Cols, maior.Rows, c2, r2, sessionName)
	}
	// Fallback: if the client reconnects WITHOUT resending a resize (no re-fit), we
	// still force a repaint using the master's current size. Cancelled at teardown
	// (attachDone) so it does not wobble ~750ms after the request has already closed.
	// The wobble ALWAYS fires in a goroutine (go wobble()) so the sync.Once NEVER
	// holds the proxy's read loop for the wobble's 150ms.
	// The SERVER wobble only arms on a FRESH attach (not a reconnect). On a reconnect
	// (attach=1) the screen is cleared by the CLIENT wobble (it resizes its own xterm
	// → reflows the browser's corrupted grid, which the server cannot reach).
	// Running both would make them fight; so the server covers the fresh attach and
	// the client covers the reattach.
	//
	// ── AND NEVER FOR A CLIENT THAT REBUILDS ITS OWN SCREEN ─────────────────
	//
	// Forcing a repaint by LYING about the geometry has already caused THREE
	// corrupted screens reported by the operator. Two are documented just above, on
	// `wobble` itself: halving the columns squeezed the history to 24 columns and
	// duplicated it. The third is halving the ROWS, and its diagnosis closes the
	// case — the pattern is not "the columns were wrong", it is that lying about
	// the geometry is not a sane way to ask for a repaint.
	//
	// The proof, from the server's RAW bytes (not from the app's drawing):
	//
	//   data/users/sam/session-logs/Aplicativo.log, offset 2504030
	//   ...^[[2C^[[8A A1gavetacsempreanavegou ^[[27Gcom^[[31GpopUpTo...
	//
	// The correct text is "A gaveta sempre navegou". The `1`, the `c` and the `a`
	// sit in the SPACES, and they come from another frame. What emitted that was
	// the remote program: the corrupted frame is in ITS buffer. That is what
	// happens when it reads 26 rows, lays out and paints, and 350 ms later reads 52,
	// lays out and paints again compositing into the same buffer with spaces treated
	// as transparent — the two layouts fuse cell by cell.
	//
	// And the journal shows this is per attach, not by chance:
	//
	//   21:42:35 [pty] repaint-wobble on attach: 67x48 → 67x24 → 67x48
	//   21:43:39 [pty] repaint-wobble on attach: 67x48 → 67x24 → 67x48
	//
	// Hence the report no other hypothesis explained: "I left and came back,
	// it's still scrambled". Leaving and coming back is a FRESH attach — that is,
	// the very gesture of trying to fix it is what reapplies the damage.
	//
	// ── AND WHY IT CAME BACK FOR CLIENTS THAT PRIME THEMSELVES ───────────
	//
	// I had turned the wobble off for the app, on the reasoning that it rebuilds
	// the screen by replaying the log and therefore did not need it. The reasoning
	// had a hole, and it only showed up by measuring the bytes the server actually
	// delivers:
	//
	// The log is a cut of a LIVE stream, taken at an arbitrary instant. The stream
	// of a differential renderer is only self-consistent at a FRAME BOUNDARY — in
	// between, it is "I wrote twelve blank lines to make room and now I am going up
	// to paint". Cut there, the replay rebuilds a HALF-PAINTED screen: blank, with
	// the content pushed into the scrollback.
	//
	// Measured: the bytes `rawLogTail` returns for the "Aplicativo" session end in
	// the middle of a table being drawn; fed into the app's own engine they yield
	// four lines of content and forty-nine blank ones. That was it all along, and it
	// is why attaching an image fixed the screen: the sheet changes the grid height,
	// the resize reaches the PTY, and the program repaints a WHOLE frame.
	//
	// In a live session the hole closes by itself a second later, as the program
	// keeps painting. In an IDLE session — which is exactly when someone opens the
	// app to look — nothing arrives, and the screen stays like that.
	//
	// So the repaint is back for every fresh attach. What changes, and what makes
	// this different from repeating the mistake, is that the nudge now GROWS instead
	// of shrinking: see the comment in the body of `wobble`.
	//
	// The decision lives in [primingDoServidor], above, and that is where it is tested.
	defer func() {
		if pararQuadro != nil {
			pararQuadro()
		}
	}()

	attachDone := make(chan struct{})
	defer close(attachDone)
	go func() {
		select {
		case <-time.After(600 * time.Millisecond):
			if forcarRepaint {
				repaintOnce.Do(func() { go wobble() })
			}
		case <-attachDone:
		}
	}()

	// `size=1` is the client saying it UNDERSTANDS the effective-size notice and
	// that it will draw the SESSION's grid. Anyone who does not ask carries on as
	// before — an old client would receive the JSON and write it to the screen.
	querAviso := r.URL.Query().Get("size") == "1"

	// pedido*: what THIS client last asked for. It serves only the instrumentation
	// below — what rules the pty is the session's EFFECTIVE size, kept in
	// last* by [aplicaTamanho]. Confusing the two is what made the
	// repaint-wobble restore this client's raw request on top of the
	// effective size, silently trampling the minimum.
	// Only the proxy's read loop touches this, and it is single-threaded.
	var pedidoCols, pedidoRows uint16
	proxy(conn, ptmx, func(cols, rows uint16) {
		// INSTRUMENTATION: every size change makes a differentially redrawing
		// app (Ink/Claude Code) repaint the WHOLE FRAME. If the frame is taller
		// than the screen, the repaint's `ESC[nA` saturates at the first line and
		// the previous copy stays — and that is the duplication the operator
		// reports, now live and not only on attach.
		//
		// The suspicion is that the phone's on-screen keyboard is shrinking the
		// grid (adjustResize + imePadding) and giving it back, one SIGWINCH per
		// open and close. The wobble log has already shown the signature: it
		// restored at 53 having left from 52. Without this record, the correlation
		// between opening the keyboard and duplicating stays a guess — with it, it
		// becomes a measurement.
		if pedidoRows != 0 && (pedidoCols != cols || pedidoRows != rows) {
			log.Printf("[pty] client resize: %dx%d → %dx%d (session %q)", pedidoCols, pedidoRows, cols, rows, sessionName)
		}
		pedidoCols, pedidoRows = cols, rows
		// This client's REAL window. In frame mode its pty sits at the SESSION's
		// size, so this is the only place holding the true size — and it is what
		// the crop is computed against.
		quadroMu.Lock()
		janelaCols, janelaRows = cols, rows
		if quadro != nil {
			quadro.redimensiona(int(cols), int(rows))
		}
		quadroMu.Unlock()
		// The PTY sits at the LARGEST among the clients that accept frame mode —
		// see `tamanho_da_sessao.go`. With a single client this is the identity;
		// with two, it is the difference between converging and fighting forever.
		if sessaoCompartilhada == nil {
			aplicaTamanho(cols, rows)
		} else {
			logsDeSessaoMu.Lock()
			efetivoCols, efetivoRows, mudou, aplicadores := sessaoCompartilhada.registraTamanho(idDaConexao, cols, rows, aceitaQuadro)
			logsDeSessaoMu.Unlock()
			if mudou {
				if efetivoCols != cols || efetivoRows != rows {
					log.Printf("[pty] effective size (smallest across clients): %dx%d (session %q)",
						efetivoCols, efetivoRows, sessionName)
				}
				// OUTSIDE the lock: applying means writing to a websocket and an
				// ioctl, and doing that while holding the session mutex is how you
				// invent a deadlock between two connections.
				for _, aplicar := range aplicadores {
					aplicar(efetivoCols, efetivoRows)
				}
			} else {
				// The SESSION has not changed, but THIS client may have just
				// arrived: its pty is born 0x0 and needs the effective size
				// either way. Returning early here is what left a new
				// connection depending on the wobble to get sized.
				aplicaTamanho(efetivoCols, efetivoRows)
			}
		}
		// first resize from the client = it has just attached → fire the repaint (fresh attach only).
		if forcarRepaint {
			repaintOnce.Do(func() { go wobble() })
		}
	}, tee, func(avisar func(uint16, uint16), escreve func([]byte) error) {
		szMu.Lock()
		escreverNoCliente = escreve
		szMu.Unlock()
		if !querAviso {
			return
		}
		// From here on [aplicaTamanho] tells the client too. And it announces what
		// is already in force: a client arriving mid-session needs to know the
		// session's size BEFORE the first byte, otherwise it draws the first frame
		// on the wrong grid.
		szMu.Lock()
		avisarCliente = avisar
		cols, rows := lastCols, lastRows
		szMu.Unlock()
		// A client that accepts frame mode does NOT get the initial notice: at this
		// instant we still do not know its window (the first resize has not arrived),
		// and telling "draw the session's grid" to a client that will end up in frame
		// mode is exactly the defect frame mode prevents. If it turns out to be big
		// enough, the notice goes out from [aplicaTamanho] right afterwards.
		if cols > 0 && rows > 0 && !aceitaQuadro && !emModoQuadro() {
			avisar(cols, rows)
		}
	}, func(p []byte, primeiro bool) []byte {
		// In frame mode the raw stream does not go to this client: it is drawn for
		// the SESSION's grid and would land entirely in the wrong place in the
		// smaller window. What draws there is the compositor (`quadro.go`).
		if emModoQuadro() {
			return nil
		}
		if primeiro && clientePrimaAPropriaTela {
			return bytes.TrimPrefix(p, limpezaDeAttachDoDtach)
		}
		return p
	}, func(x int) {
		quadroMu.Lock()
		q := quadro
		quadroMu.Unlock()
		if q == nil {
			return
		}
		colsDaSessao := 0
		if dataDir != "" {
			if tela := telaDe(dataDir, user, sessionName); tela != nil {
				colsDaSessao, _ = tela.tamanho()
			}
		}
		quadroMu.Lock()
		q.desloca(x, colsDaSessao)
		quadroMu.Unlock()
		select {
		case sinalDeQuadro <- struct{}{}:
		default:
		}
	})
}

// ContainerShell execs into a running container with a TTY.
func ContainerShell(w http.ResponseWriter, r *http.Request, cli *dockerclient.Client, id string) {
	ContainerExec(w, r, cli, id, nil, nil)
}

// ContainerExec is ContainerShell with the command (and the env) chosen by the
// caller. It was born for the recovery Claude, which needs to enter
// a NAMED session instead of a loose shell: that way the conversation survives
// a browser reconnect, which on an emergency screen is the rule and not the
// exception. An empty `cmd` keeps the old behaviour (login shell).
func ContainerExec(w http.ResponseWriter, r *http.Request, cli *dockerclient.Client, id string, cmd []string, env []string) {
	conn, err := upgrader.Upgrade(w, r, wsorigin.SecHeaders())
	if err != nil {
		return
	}
	defer conn.Close()

	ctx := context.Background()

	if len(cmd) == 0 {
		cmd = []string{"/bin/sh", "-c", "command -v bash >/dev/null 2>&1 && exec bash -l || exec sh"}
	}
	env = append([]string{"TERM=xterm-256color"}, env...)

	exec, err := cli.ContainerExecCreate(ctx, id, container.ExecOptions{
		AttachStdin: true, AttachStdout: true, AttachStderr: true, Tty: true,
		Cmd: cmd,
		Env: env,
	})
	if err != nil {
		conn.WriteMessage(websocket.TextMessage, []byte("exec create: "+err.Error()))
		return
	}
	attach, err := cli.ContainerExecAttach(ctx, exec.ID, container.ExecAttachOptions{Tty: true})
	if err != nil {
		conn.WriteMessage(websocket.TextMessage, []byte("exec attach: "+err.Error()))
		return
	}
	defer attach.Close()

	proxy(conn, wsFromHijacked{attach.Conn, attach.Reader}, func(cols, rows uint16) {
		_ = cli.ContainerExecResize(ctx, exec.ID, container.ResizeOptions{Width: uint(cols), Height: uint(rows)})
	}, nil, nil, nil, nil) // docker exec: there is no `dtach` in the path, and no screen to compose
}

// wsFromHijacked adapts hijacked connection to io.ReadWriteCloser.
type wsFromHijacked struct {
	w io.WriteCloser
	r io.Reader
}

func (h wsFromHijacked) Read(p []byte) (int, error)  { return h.r.Read(p) }
func (h wsFromHijacked) Write(p []byte) (int, error) { return h.w.Write(p) }
func (h wsFromHijacked) Close() error                { return h.w.Close() }

type resizer func(cols, rows uint16)

// tamanhoSao rejects degenerate sizes. A hidden or buggy client sending 1x1
// would make the program redraw into a single column, which is pure garbage; the
// ceiling keeps out the absurdities at the other end.
//
// ── WHAT USED TO BE HERE, AND WHY IT WENT AWAY ───────────────────────────
//
// There was a PER-CONNECTION `tamanhoAplicado` holding "the last size that
// actually reached the PTY" and swallowing repeats. The name lied: it held the
// last size THIS CONNECTION ASKED FOR, which is not what the PTY has whenever
// there is more than one client — then the PTY sits at the SMALLEST, and the
// bigger client has a dedup claiming its request "was already applied".
//
// The consequence was that there was no way back. When the small client left,
// the session's effective size grew, but the big client could no longer speak:
// its dedup blocked the re-assertion before it reached the session. Measured end
// to end — after the 80x24 client closed, the program kept painting 24x80 while
// the other drew 38x110, and the 20s heartbeat never fixed it. That is exactly
// the defect `tamanho_da_sessao.go` describes as "the loser COULD NO LONGER
// CORRECT ITSELF"; it had never gone away, it had merely gained a per-session
// reconciliation in front of it.
//
// What stops the repeated SIGWINCH now is two layers that do not lie:
//
//   - the session (`recalcula`) only reports `mudou` when the EFFECTIVE size
//     changes;
//   - the kernel compares the winsize in `tty_do_resize` and does not signal
//     when it is unchanged, so reapplying the same size is inert.
//
// Re-asserting stays cheap, which is what the recovery path needed; what no
// longer exists is the per-connection memory that blocked the correction.
func tamanhoSao(cols, rows uint16) bool {
	return cols >= 2 && rows >= 1 && cols <= 1000 && rows <= 1000
}

// proxy bridges a websocket connection and a PTY-like ReadWriteCloser.
// It enforces ping/pong keepalive, read/write deadlines and serialises all
// writes to the websocket through a single mutex so the ping goroutine and
// the PTY-output goroutine never race on the underlying conn.
// avisoDeTamanho is the control frame that tells the client the session's
// EFFECTIVE size — the smallest among the attached clients.
//
// It is the second half of the classic multiplexer rule: the server picks the
// size AND SAYS SO, and
// the client draws a grid of THAT size, not of its own window. Without this, a
// larger client renders into a grid bigger than the one the program is painting
// for, and all of the text lands in the wrong place.
//
// It goes as TEXT, not as PTY bytes, so it does not pass through the client's
// emulator: it is a conversation between server and client, not program output.
type avisoDeTamanho struct {
	Type string `json:"type"`
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

// filtroDeSaida decides what of the PTY reaches THIS connection. It receives the
// chunk and whether it is the FIRST of this connection; returning nil/empty
// swallows the chunk.
//
// Two uses, and both have to be per connection:
//
//   - the first chunk carries the "erase everything" that `dtach` sends to an
//     arriving client, and a client that primes its own screen must not get it
//     (it would erase what it has painted);
//   - in frame mode (`quadro.go`) the raw stream is drawn for the SESSION's
//     grid and would land entirely in the wrong place in the client's smaller
//     window — what draws there is the compositor, not the PTY.
type filtroDeSaida func(p []byte, primeiro bool) []byte

func proxy(conn *websocket.Conn, rwc io.ReadWriteCloser, resize resizer, tee io.Writer, aoPoderEscrever func(avisarTamanho func(uint16, uint16), escreverBytes func([]byte) error), filtra filtroDeSaida, desloca func(int)) {
	conn.SetReadLimit(maxMessage)
	_ = conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		_ = conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	// Record the connection as live so that a SIGTERM (deploy) can warn it
	// before the process dies — see restart.go.
	defer registerLive(conn)()

	var mu sync.Mutex
	writeBinary := func(b []byte) error {
		mu.Lock()
		defer mu.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
		return conn.WriteMessage(websocket.BinaryMessage, b)
	}
	// Only clients that ASKED for the notice receive it — see `HostShell`. An old
	// client that does not understand the frame would write JSON to the screen.
	if aoPoderEscrever != nil {
		aoPoderEscrever(func(cols, rows uint16) {
			b, err := json.Marshal(avisoDeTamanho{Type: "size", Cols: cols, Rows: rows})
			if err != nil {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
			_ = conn.WriteMessage(websocket.TextMessage, b)
		}, writeBinary)
	}
	writePing := func() error {
		mu.Lock()
		defer mu.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
		return conn.WriteMessage(websocket.PingMessage, nil)
	}

	done := make(chan struct{})
	closeOnce := sync.Once{}
	shutdown := func() {
		closeOnce.Do(func() {
			_ = rwc.Close()
			close(done)
		})
	}

	// ── Flow control (backpressure) ───────────────────────────────────────
	// The client (xterm.js) says when its write buffer is full ({type:pause}) or has
	// drained again ({type:resume}). On pause, the PTY->WS pump STOPS reading the rwc
	// → the OS-PTY buffer fills → the program blocks on write and stops producing
	// (real backpressure, no bytes dropped). Without this, heavy output (Claude
	// streaming) overruns xterm's 50MB buffer, which then DISCARDS data =
	// corruption/missing characters. This is the official xterm.js pattern (its Flow
	// Control guide).
	var (
		fcMu     sync.Mutex
		fcPaused bool
		fcResume = make(chan struct{}) // (re)created on every pause; closed on resume
	)
	setPaused := func(p bool) {
		fcMu.Lock()
		if p && !fcPaused {
			fcPaused = true
			fcResume = make(chan struct{})
		} else if !p && fcPaused {
			fcPaused = false
			close(fcResume)
		}
		fcMu.Unlock()
	}
	waitIfPaused := func() {
		for {
			fcMu.Lock()
			if !fcPaused {
				fcMu.Unlock()
				return
			}
			ch := fcResume
			fcMu.Unlock()
			select {
			case <-ch: // retomado
			case <-done:
				return
			case <-time.After(maxPauseDuration):
				// Defence against a stuck client: if the xterm has not resumed within
				// maxPauseDuration (frozen renderer, a write callback that never
				// fired), auto-resume so the terminal is NOT left frozen forever. If
				// the client is still overloaded it pauses again on the next
				// message — so this is safe.
				setPaused(false)
				log.Printf("[pty] flow control: auto-resume after %v paused (the client never resumed)", maxPauseDuration)
				return
			}
		}
	}

	// PTY -> WS pump — panic recovery: pty.Read can raise on a closed fd.
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[pty] PTY->WS pump panic recovered: %v", r)
				shutdown()
			}
		}()
		buf := make([]byte, readChunk)
		primeiroBloco := true
		for {
			waitIfPaused() // backpressure: bloqueia enquanto o cliente pediu pausa
			n, err := rwc.Read(buf)
			if n > 0 {
				// Tee into the session log BEFORE the WS: best-effort, error ignored
				// (sessionLogWriter.Write never really fails). Only the pty's output
				// is logged — user input does not pass through here.
				if tee != nil {
					_, _ = tee.Write(buf[:n])
				}
				saida := buf[:n]
				if filtra != nil {
					saida = filtra(saida, primeiroBloco)
				}
				primeiroBloco = false
				if len(saida) > 0 {
					if werr := writeBinary(saida); werr != nil {
						shutdown()
						return
					}
				}
			}
			if err != nil {
				shutdown()
				return
			}
		}
	}()

	// ping ticker — keeps NAT/proxy idle timers from killing the connection
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[pty] ping ticker panic recovered: %v", r)
				shutdown()
			}
		}()
		t := time.NewTicker(pingPeriod)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				if err := writePing(); err != nil {
					shutdown()
					return
				}
			}
		}
	}()

	// WS -> PTY pump (this goroutine is the function's main loop)
	for {
		mt, data, err := conn.ReadMessage()
		if err != nil {
			shutdown()
			return
		}
		switch mt {
		case websocket.TextMessage:
			if len(data) > 0 && data[0] == '{' {
				var m ctrlMsg
				if jerr := json.Unmarshal(data, &m); jerr == nil {
					switch m.Type {
					case "resize":
						// No per-connection dedup: what reaches the PTY is decided
						// by the session. See [tamanhoSao].
						if resize != nil && tamanhoSao(m.Cols, m.Rows) {
							resize(m.Cols, m.Rows)
						}
					case "pan":
						// Pans this client's crop horizontally. Inert outside
						// frame mode.
						if desloca != nil {
							desloca(m.X)
						}
					case "input":
						_, _ = rwc.Write([]byte(m.Data))
					case "paste":
						// Bracketed paste (xterm and mosh already do this): the
						// pasted content arrives wrapped in ESC[200~ ... ESC[201~
						// in a SINGLE Write, so readline shells (bash/zsh) and
						// apps like vim treat it as ONE atomic block — without the
						// line-by-line auto-indent that raw "input" would produce.
						// The server does not track whether the app on the other
						// side has bracketed paste enabled; the terminal/app itself
						// decides, exactly as with any real xterm client.
						_, _ = rwc.Write([]byte("\x1b[200~" + m.Data + "\x1b[201~"))
					case "pause":
						// flow control: client buffer full → stop reading the PTY
						setPaused(true)
					case "resume":
						setPaused(false)
					case "ping":
						// Application-level heartbeat from the browser. ANSWERING is not
						// optional: the client watchdog closes the
						// connection with code 4000 after 70s without ANY
						// message, and PROTOCOL pings/pongs are invisible to
						// JavaScript. On an IDLE pane — user reading or typing
						// without producing output — nothing arrived, and the watchdog killed a
						// HEALTHY connection every ~70s. That was the "it keeps reconnecting
						// by itself": self-inflicted, not a network problem.
						//
						// The answer is a deliberately EMPTY binary frame. The
						// client writes to the terminal everything that arrives, so a pong
						// in JSON would become garbage on screen for any client with the old
						// JS still cached. Zero bytes is invisible when written and
						// still counts as a "message" for the watchdog — the
						// fix works for new AND old clients.
						_ = writeBinary(nil)
					}
					// Valid JSON (with or without a known type) NEVER falls through
					// to the fallback Write — otherwise a well-formed
					// {"type":"foo"} would leak into the session as literal text.
					continue
				}
			}
			_, _ = rwc.Write(data)
		case websocket.BinaryMessage:
			_, _ = rwc.Write(data)
		}
	}
}
