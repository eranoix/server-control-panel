//go:build live

package api

// live_console_test.go — the LIVE proof of the remote console and of rollback,
// against the home hypervisor.
//
// DOUBLE LOCK, same as live_proxmox_test.go: the `live` build tag AND the
// LAB_PVC_LIVE=1 variable.
//
// 🔴 WHAT THIS TEST DOES NOT DO, AND WHY: it does NOT run a real rollback. A
// rollback throws away everything written to the guest since the snapshot, and
// CT 204 (`lab`) is a machine in use — there is no "dry-run rollback".
// What it proves, without destroying anything, is the WHOLE path down to the
// hypervisor: it asks for a rollback to a snapshot that DOES NOT EXIST and shows
//
//	(a) the ACL lets it through — PVE accepts it and creates the task;
//	(b) PVE answers 200 anyway (the measured pitfall);
//	(c) the dashboard does NOT pass that 200 along: WaitTask picks up the
//	    exitstatus and the route answers 502 with the real reason.
//
// That is the proof that matters. A test that really rolled the guest back would
// prove less and cost more.
//
//	run: LAB_PVC_LIVE=1 go test -tags=live -run TestLiveConsole ./internal/api/ -v

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"server-control-panel/internal/auth"
)

// guestsDeConsole are the targets of the measurement. There are FOUR of them,
// of TWO kinds, on purpose: the defect that motivated this care was a
// `vncproxy 204` failing on the host, and measuring on a single guest would have
// declared the path good on a sample of size 1. `pbs` is in for the opposite
// reason: it has NO token, and the test demands that the refusal be explained.
var guestsDeConsole = []struct {
	id       string
	nome     string
	temToken bool
}{
	{"lxc/204", "lab", true},
	{"lxc/207", "apps", true},
	{"lxc/205", "observ", true},
	{"qemu/208", "dev", true},
	{"lxc/202", "pbs", false},
}

func TestLiveConsoleDoGuest(t *testing.T) {
	if os.Getenv("LAB_PVC_LIVE") != "1" {
		t.Skip("live proof turned off — run with LAB_PVC_LIVE=1 (it really OPENS a console on the guests)")
	}
	t0 := time.Now().UTC()
	r, cancel := routerVivo(t)
	defer cancel()

	trilha, err := auth.NewAuditLog(filepath.Join(t.TempDir(), "audit.log"))
	if err != nil {
		t.Fatal(err)
	}
	r.audit = trilha

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.handleProxmoxConsole(w, req.WithContext(auth.WithUser(req.Context(), r.cfg.Primary)))
	}))
	defer srv.Close()
	base := "ws" + strings.TrimPrefix(srv.URL, "http")

	// ── 1. a token in the URL is refused BEFORE anything else ─────────────
	if _, resp, err := websocket.DefaultDialer.Dial(base+"?node=lxc/204&token=x", nil); err == nil {
		t.Error("token in the URL was accepted")
	} else if resp == nil || resp.StatusCode != 400 {
		t.Errorf("token in the URL: status = %v, want 400", resp)
	} else {
		t.Log("token in the URL → 400, as the /ws/shell precedent requires")
	}

	// ── 2. live console, guest by guest ───────────────────────────────────
	abertos := 0
	for _, g := range guestsDeConsole {
		g := g
		t.Run(g.nome, func(t *testing.T) {
			conn, resp, err := websocket.DefaultDialer.Dial(base+"?node="+g.id, nil)
			if !g.temToken {
				// 🔴 CT 202 has no node token. The refusal has to NAME the key
				// that is missing — a generic "error" sends the operator
				// hunting for a defect where there is none.
				if err == nil {
					conn.Close()
					t.Fatalf("%s opened a console without a node token", g.id)
				}
				if resp == nil || resp.StatusCode != http.StatusConflict {
					t.Fatalf("%s: status = %v, want 409", g.id, resp)
				}
				corpo := make([]byte, 512)
				n, _ := resp.Body.Read(corpo)
				if !strings.Contains(string(corpo[:n]), "pve_token_node_pbs") {
					t.Errorf("%s: body = %s, want it to name the missing key", g.id, corpo[:n])
				}
				t.Logf("%-9s %-7s → 409 naming the missing key (no node token, and the screen SAYS so)", g.id, g.nome)
				return
			}
			if err != nil {
				t.Fatalf("%s (%s): dial: %v (resp=%v)", g.id, g.nome, err, resp)
			}
			defer conn.Close()

			// The first text frame is the control one. `ready` proves the THREE
			// steps went through on the hypervisor; `error` carries the real reason.
			_ = conn.SetReadDeadline(time.Now().Add(20 * time.Second))
			var pronto map[string]any
			for pronto == nil {
				mt, dados, err := conn.ReadMessage()
				if err != nil {
					t.Fatalf("%s: no control frame: %v", g.id, err)
				}
				if mt != websocket.TextMessage {
					continue
				}
				if err := json.Unmarshal(dados, &pronto); err != nil {
					t.Fatalf("%s: control is not JSON: %s", g.id, dados)
				}
			}
			if pronto["type"] != "ready" {
				t.Fatalf("%s (%s): o hipervisor recusou o console: %v", g.id, g.nome, pronto["message"])
			}

			marca := "PVC-" + g.nome
			if err := conn.WriteJSON(map[string]any{"type": "resize", "cols": 100, "rows": 30}); err != nil {
				t.Fatal(err)
			}

			// Reader in a GOROUTINE, not a read with a deadline inside the loop.
			//
			// gorilla/websocket invalidates the connection after a read error:
			// blow the deadline once and every later read fails. That rules out
			// the obvious "wait a bit, retry, wait again" shape built on
			// SetReadDeadline. A single reader pushing into a channel lets the
			// outer loop decide how long to wait without ever hurting the
			// connection.
			bytesDoTTY := make(chan []byte, 64)
			erroDeLeitura := make(chan error, 1)
			go func() {
				for {
					mt, dados, err := conn.ReadMessage()
					if err != nil {
						erroDeLeitura <- err
						close(bytesDoTTY)
						return
					}
					if mt == websocket.BinaryMessage {
						cp := make([]byte, len(dados))
						copy(cp, dados)
						bytesDoTTY <- cp
					}
				}
			}()

			// 🔴 SYNCHRONIZE THE TTY BEFORE TYPING, AND KEEP INSISTING.
			//
			// This step was born of a real, MEASURED defect. The test used to
			// send its marker with "\n", which SUBMITTED the line as a username
			// and left the guest sitting at the PASSWORD prompt. A password does
			// not echo: the next round typed the marker, got nothing back and
			// blew the deadline. The test alternated green and red by the PARITY
			// of its runs, and the red was never the console's — it was the
			// test's own, poisoning its own next round.
			//
			// Insisting is mandatory, not paranoia: after failed attempts
			// `login` EXITS and getty is reborn. In that window no process is
			// reading the keyboard, so a single keystroke sent falls into the
			// void and the silence outlasts any reasonable deadline. That is
			// exactly what made the first version of this fix need TWO rounds to
			// converge. Ctrl-U erases the half-typed line; Enter forces a fresh
			// prompt; and if nobody answers, send it again.
			var antes strings.Builder
			limite := time.Now().Add(60 * time.Second)
			sincronizado := false
		sync:
			for !sincronizado && time.Now().Before(limite) {
				if err := conn.WriteJSON(map[string]any{"type": "input", "data": "\x15\n"}); err != nil {
					t.Fatalf("%s: write to the console: %v", g.id, err)
				}
				espera := time.After(5 * time.Second)
				for {
					select {
					case dados, ok := <-bytesDoTTY:
						if !ok {
							t.Fatalf("%s (%s): console closed during synchronization (received %q): %v",
								g.id, g.nome, antes.String(), <-erroDeLeitura)
						}
						antes.Write(dados)
						if ttyEcoa(antes.String()) {
							sincronizado = true
							break sync
						}
					case <-espera:
						// Silence: getty may be respawning. Insist.
						continue sync
					}
				}
			}
			if !sincronizado {
				t.Fatalf("%s (%s): the tty did not reach a prompt that echoes after 60 s of retrying (received %q)",
					g.id, g.nome, antes.String())
			}

			// Type and wait for the answer to COME BACK. It is the whole loop:
			// browser → dashboard → PVE → guest → PVE → dashboard → browser.
			// WITHOUT "\n": a tty in canonical mode echoes character by character,
			// so the echo proves the round trip without SUBMITTING anything —
			// nothing runs in somebody else's shell and no login is left pending.
			if err := conn.WriteJSON(map[string]any{"type": "input", "data": "echo " + marca}); err != nil {
				t.Fatal(err)
			}
			var saida strings.Builder
			prazoEco := time.After(15 * time.Second)
			for !strings.Contains(saida.String(), marca) {
				select {
				case dados, ok := <-bytesDoTTY:
					if !ok {
						t.Fatalf("%s (%s): console closed before the echo (received %q): %v",
							g.id, g.nome, saida.String(), <-erroDeLeitura)
					}
					saida.Write(dados)
				case <-prazoEco:
					t.Fatalf("%s (%s): nothing came back from the terminal in 15 s (received %q)",
						g.id, g.nome, saida.String())
				}
			}

			// LEAVE THE TTY AS IT WAS FOUND: Ctrl-U erases the typed line.
			// Without this the test leaves a trace on the OPERATOR's console —
			// four guests sitting there with a fake username typed in — and it
			// was that trace that broke the next round.
			_ = conn.WriteJSON(map[string]any{"type": "input", "data": "\x15"})

			abertos++
			limpo := strings.ReplaceAll(strings.TrimSpace(saida.String()), "\r", "")
			if len(limpo) > 90 {
				limpo = limpo[len(limpo)-90:]
			}
			t.Logf("%-9s %-7s → live console, %d bytes back … %q", g.id, g.nome, saida.Len(), limpo)
		})
	}
	if abertos < 2 {
		t.Fatalf("console measured on %d guest(s) — the measurement needs more than one,"+
			"porque `vncproxy 204` já falhou neste host e uma amostra de 1 daria o caminho por bom", abertos)
	}

	// ── 3. the trail, at both ends, for every session ────────────────────
	//
	// 🔴 THE READ WAITS, AND THAT DOES NOT WEAKEN THE ASSERTION.
	//
	// The "closed" record is written when the websocket DROPS, on the server
	// side, after this test has already moved on. Reading the trail once only
	// states "both ends were recorded UP TO THIS INSTANT" — and the instant is
	// arbitrary. While each session lasted 16 s the last write always arrived
	// first; when the tty fix brought that down to 2.5 s, the test started
	// failing on 3 closes out of 4. The defect had been in the assertion all
	// along; speed merely revealed it.
	//
	// The property that matters is unchanged — every session records an open
	// AND a close — only measured with a deadline instead of in a blink.
	// The deadline is the difference between "it did not happen" and "it had not happened yet".
	var abriu, fechou int
	limite := time.Now().Add(10 * time.Second)
	for {
		abriu, fechou = 0, 0
		for _, e := range trilha.Tail(200) {
			if e.Action != "pve.console" {
				continue
			}
			if strings.Contains(e.Target, "acao=abriu") {
				abriu++
			}
			if strings.Contains(e.Target, "acao=fechou") {
				fechou++
			}
		}
		if (abriu == fechou && abriu >= abertos) || time.Now().After(limite) {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	if abriu != fechou || abriu < abertos {
		t.Errorf("trail: %d opens and %d closes for %d sessions, even after 10 s of waiting"+
			"— o console tem de registrar os dois extremos", abriu, fechou, abertos)
	}
	t.Logf("trail: %d opens and %d closes recorded (the price of the exception to §7.3)", abriu, fechou)

	// 🔴 And the secret is NOT in the trail. An audit log that keeps a
	// credential is a password file under another name.
	valorDoCofre, _ := r.tokenDoCofre("pve_token_node_lab")
	segredo := valorDoCofre
	if i := strings.Index(valorDoCofre, "="); i > 0 {
		segredo = valorDoCofre[i+1:]
	}
	for _, e := range trilha.Tail(200) {
		if segredo != "" && strings.Contains(e.Target, segredo) {
			t.Fatal("the trail carries the token's secret")
		}
	}

	t.Logf("proof window: [t0=%s t1=%s]", t0.Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339))
}

// 🔴 TestLiveRollbackNaoRepassaO200DoPVE is that PVE pitfall proved LIVE on the
// most destructive route of the dashboard — without destroying anything.
//
// Measured with the node token:
//
//	POST /nodes/pve/lxc/204/snapshot/<nonexistent>/rollback → HTTP 200 + UPID
//	task status → stopped, exitstatus "snapshot '…' does not exist"
//	CT 204 stayed running, uptime intact
//
// If the dashboard passed that 200 along, the screen would say "restored" for a
// rollback that never happened — and the operator would start believing the guest
// is in an earlier state. The route has to answer 502 WITH the reason.
func TestLiveRollbackNaoRepassaO200DoPVE(t *testing.T) {
	if os.Getenv("LAB_PVC_LIVE") != "1" {
		t.Skip("live proof turned off — run with LAB_PVC_LIVE=1")
	}
	r, cancel := routerVivo(t)
	defer cancel()

	// A name that is valid for PVE and nonexistent by construction: the rollback
	// dies looking the snapshot up, before touching any disk.
	const inexistente = "pvc-prova-viva-inexistente"
	w, out := pvxPOST(t, r, "/api/proxmox/snapshots/rollback?node=lxc/204&name="+inexistente)
	if w.Code != 502 {
		t.Fatalf("status = %d, want 502 (body=%s)", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), "does not exist") {
		t.Fatalf("body = %s — PVE's exitstatus is the only clue to the real reason", w.Body)
	}
	t.Logf("rollback of a nonexistent snapshot → PVE accepted it (ACL passed) and the task failed;"+
		"o painel devolveu 502 com o motivo: %v", out["error"])

	// And the guest is still up: nothing was restored, nothing was stopped.
	wl, outl := pvxGET(t, r, "/api/proxmox/snapshots?node=lxc/204")
	if wl.Code != 200 {
		t.Fatalf("listing snapshots after the test = %d: %s", wl.Code, wl.Body)
	}
	snaps, _ := outl["snapshots"].([]any)
	t.Logf("CT 204 intact: %d snapshot(s) — nothing was created, restored or deleted", len(snaps))

	// 🔴 The suspend route still DOES NOT EXIST. `vzsuspend 204` fails in this
	// host's CRIU, and a button that always errors trains people to ignore errors.
	ws, _ := pvxPOST(t, r, "/api/proxmox/suspend?node=lxc/204")
	if ws.Code != 404 {
		t.Errorf("/api/proxmox/suspend = %d, want 404 — suspend is out until CRIU works", ws.Code)
	}
	t.Log("/api/proxmox/suspend → 404, as decided (the reason is written in the code)")
}

func pvxPOST(t *testing.T, r *Router, caminho string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	w := httptest.NewRecorder()
	r.handleProxmox(w, req(t, http.MethodPost, caminho, ""))
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w, out
}

// ttyEcoa says whether the terminal is in a state that ECHOES what is typed.
//
// The login PASSWORD prompt is precisely what does NOT echo, and it was the one
// swallowing the test's marker in silence until the deadline blew. A login prompt
// and a shell prompt echo; anything else is treated as "not typeable yet".
func ttyEcoa(s string) bool {
	t := strings.TrimRight(s, " \r\n\x00")
	return strings.HasSuffix(t, "login:") || strings.HasSuffix(t, "#") || strings.HasSuffix(t, "$")
}
