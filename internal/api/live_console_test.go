//go:build live

package api

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

var consoleGuests = []struct {
	id       string
	name     string
	hasToken bool
}{
	{"lxc/204", "lab", true},
	{"lxc/207", "apps", true},
	{"lxc/205", "observ", true},
	{"qemu/208", "dev", true},
	{"lxc/202", "pbs", false},
}

func TestLiveGuestConsole(t *testing.T) {
	if os.Getenv("LAB_PVC_LIVE") != "1" {
		t.Skip("live proof turned off — run with LAB_PVC_LIVE=1 (it really OPENS a console on the guests)")
	}
	t0 := time.Now().UTC()
	r, cancel := liveRouter(t)
	defer cancel()

	trail, err := auth.NewAuditLog(filepath.Join(t.TempDir(), "audit.log"))
	if err != nil {
		t.Fatal(err)
	}
	r.audit = trail

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.handleProxmoxConsole(w, req.WithContext(auth.WithUser(req.Context(), r.cfg.Primary)))
	}))
	defer srv.Close()
	base := "ws" + strings.TrimPrefix(srv.URL, "http")

	if _, resp, err := websocket.DefaultDialer.Dial(base+"?node=lxc/204&token=x", nil); err == nil {
		t.Error("token in the URL was accepted")
	} else if resp == nil || resp.StatusCode != 400 {
		t.Errorf("token in the URL: status = %v, want 400", resp)
	} else {
		t.Log("token in the URL → 400, as the /ws/shell precedent requires")
	}

	opened := 0
	for _, g := range consoleGuests {
		g := g
		t.Run(g.name, func(t *testing.T) {
			conn, resp, err := websocket.DefaultDialer.Dial(base+"?node="+g.id, nil)
			if !g.hasToken {
				if err == nil {
					conn.Close()
					t.Fatalf("%s opened a console without a node token", g.id)
				}
				if resp == nil || resp.StatusCode != http.StatusConflict {
					t.Fatalf("%s: status = %v, want 409", g.id, resp)
				}
				body := make([]byte, 512)
				n, _ := resp.Body.Read(body)
				if !strings.Contains(string(body[:n]), "pve_token_node_pbs") {
					t.Errorf("%s: body = %s, want it to name the missing key", g.id, body[:n])
				}
				t.Logf("%-9s %-7s → 409 naming the missing key (no node token, and the screen SAYS so)", g.id, g.name)
				return
			}
			if err != nil {
				t.Fatalf("%s (%s): dial: %v (resp=%v)", g.id, g.name, err, resp)
			}
			defer conn.Close()

			_ = conn.SetReadDeadline(time.Now().Add(20 * time.Second))
			var ready map[string]any
			for ready == nil {
				mt, data, err := conn.ReadMessage()
				if err != nil {
					t.Fatalf("%s: no control frame: %v", g.id, err)
				}
				if mt != websocket.TextMessage {
					continue
				}
				if err := json.Unmarshal(data, &ready); err != nil {
					t.Fatalf("%s: control is not JSON: %s", g.id, data)
				}
			}
			if ready["type"] != "ready" {
				t.Fatalf("%s (%s): the hypervisor refused the console: %v", g.id, g.name, ready["message"])
			}

			mark := "PVC-" + g.name
			if err := conn.WriteJSON(map[string]any{"type": "resize", "cols": 100, "rows": 30}); err != nil {
				t.Fatal(err)
			}

			ttyBytes := make(chan []byte, 64)
			readErr := make(chan error, 1)
			go func() {
				for {
					mt, data, err := conn.ReadMessage()
					if err != nil {
						readErr <- err
						close(ttyBytes)
						return
					}
					if mt == websocket.BinaryMessage {
						cp := make([]byte, len(data))
						copy(cp, data)
						ttyBytes <- cp
					}
				}
			}()

			var before strings.Builder
			limit := time.Now().Add(60 * time.Second)
			synced := false
		sync:
			for !synced && time.Now().Before(limit) {
				if err := conn.WriteJSON(map[string]any{"type": "input", "data": "\x15\n"}); err != nil {
					t.Fatalf("%s: write to the console: %v", g.id, err)
				}
				timeout := time.After(5 * time.Second)
				for {
					select {
					case data, ok := <-ttyBytes:
						if !ok {
							t.Fatalf("%s (%s): console closed during synchronization (received %q): %v",
								g.id, g.name, before.String(), <-readErr)
						}
						before.Write(data)
						if ttyEchoes(before.String()) {
							synced = true
							break sync
						}
					case <-timeout:
						continue sync
					}
				}
			}
			if !synced {
				t.Fatalf("%s (%s): the tty did not reach a prompt that echoes after 60 s of retrying (received %q)",
					g.id, g.name, before.String())
			}

			if err := conn.WriteJSON(map[string]any{"type": "input", "data": "echo " + mark}); err != nil {
				t.Fatal(err)
			}
			var output strings.Builder
			echoDeadline := time.After(15 * time.Second)
			for !strings.Contains(output.String(), mark) {
				select {
				case data, ok := <-ttyBytes:
					if !ok {
						t.Fatalf("%s (%s): console closed before the echo (received %q): %v",
							g.id, g.name, output.String(), <-readErr)
					}
					output.Write(data)
				case <-echoDeadline:
					t.Fatalf("%s (%s): nothing came back from the terminal in 15 s (received %q)",
						g.id, g.name, output.String())
				}
			}

			_ = conn.WriteJSON(map[string]any{"type": "input", "data": "\x15"})

			opened++
			clean := strings.ReplaceAll(strings.TrimSpace(output.String()), "\r", "")
			if len(clean) > 90 {
				clean = clean[len(clean)-90:]
			}
			t.Logf("%-9s %-7s → live console, %d bytes back … %q", g.id, g.name, output.Len(), clean)
		})
	}
	if opened < 2 {
		t.Fatalf("console measured on %d guest(s) — the measurement needs more than one,"+
			"because `vncproxy 204` has already failed on this host and a sample of 1 would pass the path as good", opened)
	}

	var opens, closes int
	limit := time.Now().Add(10 * time.Second)
	for {
		opens, closes = 0, 0
		for _, e := range trail.Tail(200) {
			if e.Action != "pve.console" {
				continue
			}
			if strings.Contains(e.Target, "action=opened") {
				opens++
			}
			if strings.Contains(e.Target, "action=closed") {
				closes++
			}
		}
		if (opens == closes && opens >= opened) || time.Now().After(limit) {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	if opens != closes || opens < opened {
		t.Errorf("trail: %d opens and %d closes for %d sessions, even after 10 s of waiting"+
			": the console has to record both ends", opens, closes, opened)
	}
	t.Logf("trail: %d opens and %d closes recorded (the price of the exception to §7.3)", opens, closes)

	vaultValue, _ := r.vaultToken("pve_token_node_lab")
	secret := vaultValue
	if i := strings.Index(vaultValue, "="); i > 0 {
		secret = vaultValue[i+1:]
	}
	for _, e := range trail.Tail(200) {
		if secret != "" && strings.Contains(e.Target, secret) {
			t.Fatal("the trail carries the token's secret")
		}
	}

	t.Logf("proof window: [t0=%s t1=%s]", t0.Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339))
}

func TestLiveRollbackDoesNotRelayPVE200(t *testing.T) {
	if os.Getenv("LAB_PVC_LIVE") != "1" {
		t.Skip("live proof turned off — run with LAB_PVC_LIVE=1")
	}
	r, cancel := liveRouter(t)
	defer cancel()

	const nonexistent = "pvc-live-probe-nonexistent"
	w, out := pvxPOST(t, r, "/api/proxmox/snapshots/rollback?node=lxc/204&name="+nonexistent)
	if w.Code != 502 {
		t.Fatalf("status = %d, want 502 (body=%s)", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), "does not exist") {
		t.Fatalf("body = %s — PVE's exitstatus is the only clue to the real reason", w.Body)
	}
	t.Logf("rollback of a nonexistent snapshot → PVE accepted it (ACL passed) and the task failed;"+
		"the panel returned 502 with the reason: %v", out["error"])

	wl, outl := pvxGET(t, r, "/api/proxmox/snapshots?node=lxc/204")
	if wl.Code != 200 {
		t.Fatalf("listing snapshots after the test = %d: %s", wl.Code, wl.Body)
	}
	snaps, _ := outl["snapshots"].([]any)
	t.Logf("CT 204 intact: %d snapshot(s) — nothing was created, restored or deleted", len(snaps))

	ws, _ := pvxPOST(t, r, "/api/proxmox/suspend?node=lxc/204")
	if ws.Code != 404 {
		t.Errorf("/api/proxmox/suspend = %d, want 404 — suspend is out until CRIU works", ws.Code)
	}
	t.Log("/api/proxmox/suspend → 404, as decided (the reason is written in the code)")
}

func pvxPOST(t *testing.T, r *Router, path string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	w := httptest.NewRecorder()
	r.handleProxmox(w, req(t, http.MethodPost, path, ""))
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w, out
}

func ttyEchoes(s string) bool {
	t := strings.TrimRight(s, " \r\n\x00")
	return strings.HasSuffix(t, "login:") || strings.HasSuffix(t, "#") || strings.HasSuffix(t, "$")
}
