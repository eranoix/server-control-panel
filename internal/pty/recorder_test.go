package pty

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// serverWithRecorder brings up a real HostShell over a dataDir of its own.
func serverWithRecorder(t *testing.T, name string) (dir string, dial func(string) *websocket.Conn) {
	t.Helper()
	if _, err := exec.LookPath("dtach"); err != nil {
		t.Skip("no dtach on this machine")
	}
	dir = t.TempDir()
	own, err := LoadOwnership(dir + "/own.json")
	if err != nil {
		t.Fatal(err)
	}
	reg, err := LoadRegistry(dir + "/reg.json")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		HostShell(w, r, "u", true, own, "", dir, reg)
	}))
	t.Cleanup(func() {
		StopRecorder(dir, "u", name)
		srv.Close()
		_ = exec.Command("pkill", "-f", socketPathFor(dir, name)).Run()
	})
	return dir, func(extra string) *websocket.Conn {
		u := "ws" + strings.TrimPrefix(srv.URL, "http") + "/?size=1&name=" + name + extra
		c, _, err := websocket.DefaultDialer.Dial(u, nil)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		go func() {
			for {
				if _, _, err := c.ReadMessage(); err != nil {
					return
				}
			}
		}()
		_ = c.WriteMessage(websocket.TextMessage, []byte(`{"type":"resize","cols":80,"rows":24}`))
		return c
	}
}

// WHAT HAPPENS WITH NOBODY ATTACHED HAS TO GO INTO THE LOG.
//
// Before the recorder, the tee lived inside the connection: close the tab and the
// session stayed alive with its output written nowhere. This very test, run
// before the fix, lost all five markers — and reattaching recovered none of them.
// It is the interval in which a person closes the laptop and moves to another
// computer, that is, exactly the stretch they come back wanting to read.
func TestRecorderLeavesNoLogGapWithNobodyAttached(t *testing.T) {
	name := "recorder-gap"
	dir, dial := serverWithRecorder(t, name)

	c := dial("")
	time.Sleep(1500 * time.Millisecond)
	// Five markers, one every 2s — all AFTER I left.
	cmd := `(for i in 1 2 3 4 5; do sleep 2; echo MARK_$i; done) &` + "\r"
	_ = c.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"type":"input","data":%q}`, cmd)))
	time.Sleep(600 * time.Millisecond)
	_ = c.Close()

	time.Sleep(14 * time.Second) // the markers come out with nobody attached

	data, _ := os.ReadFile(sessionLogPath(dir, "u", name))
	var missing []string
	for i := 1; i <= 5; i++ {
		m := fmt.Sprintf("MARK_%d", i)
		if !strings.Contains(string(data), m) {
			missing = append(missing, m)
		}
	}
	if len(missing) > 0 {
		t.Errorf("lost from the log with nobody attached: %v — the recorder is not holding the session", missing)
	}
}

// A session with the recorder attached still responds to the real client's size
// — the recorder is invisible to the minimum rule.
func TestSessionWithRecorderStillFollowsRealClient(t *testing.T) {
	dial := testSession(t, "recorder-min")

	client := dial()
	client.resize(100, 30)
	time.Sleep(1800 * time.Millisecond)
	if r, c := client.programSize(); r != "30" || c != "100" {
		t.Fatalf("program sees %sx%s; wanted 30x100 — the recorder is being opinionated about the size", r, c)
	}

	client.resize(70, 20)
	time.Sleep(1500 * time.Millisecond)
	if r, c := client.programSize(); r != "20" || c != "70" {
		t.Errorf("program sees %sx%s; wanted 20x70", r, c)
	}
}

// THE WHOLE CHAIN: a live session -> the recorder -> the server's emulator ->
// the history file -> what the panel fetches when it opens the session.
//
// Every piece has a test of its own; this is the only one that proves they are
// WIRED TOGETHER. It was exactly one correct piece with a cut wire (the
// `forgetSize` whose return value nobody used) that let the original defect
// slip through a green battery — see session_size_e2e_test.go.
func TestHistoryFlowsFromLiveSessionToPanelFetch(t *testing.T) {
	name := "hist-chain"
	dir, dial := serverWithRecorder(t, name)

	// SessionHistory reads from the package's ACTIVE dataDir.
	reg, err := LoadRegistry(dir + "/reg-active.json")
	if err != nil {
		t.Fatal(err)
	}
	InitSessionBackend(dir, reg)

	c := dial("")
	time.Sleep(1500 * time.Millisecond)
	// Write more lines than fit on the screen (24): the excess scrolls off and
	// that is what becomes history.
	cmd := `for i in $(seq 1 60); do echo LINE_DE_HISTORY_$i; done` + "\r"
	_ = c.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"type":"input","data":%q}`, cmd)))
	time.Sleep(3 * time.Second)
	_ = c.Close()
	time.Sleep(1500 * time.Millisecond)

	data, total := SessionHistory("u", name, 1<<20)
	if total == 0 {
		t.Fatal("the session history is empty — the recorder→emulator→file chain is cut")
	}
	text := stripANSI(string(data))
	missing := 0
	for i := 1; i <= 20; i++ { // the first ones have certainly scrolled out by now
		if !strings.Contains(text, fmt.Sprintf("LINE_DE_HISTORY_%d", i)) {
			missing++
		}
	}
	if missing > 2 {
		t.Errorf("%d of the first 20 lines are not in the history (%d bytes read)", missing, total)
	}
}
