package pty

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

var reProgramSize = regexp.MustCompile(`(?m)^\s*(\d{1,3}) (\d{1,3})\s*$`)

type testClient struct {
	conn *websocket.Conn
	t    *testing.T

	mu       sync.Mutex
	warnings []sizeNotice
}

func (c *testClient) send(v any) {
	c.t.Helper()
	b, _ := json.Marshal(v)
	if err := c.conn.WriteMessage(websocket.TextMessage, b); err != nil {
		c.t.Fatalf("write: %v", err)
	}
}

func (c *testClient) resize(cols, rows int) {
	c.send(map[string]any{"type": "resize", "cols": cols, "rows": rows})
}

func (c *testClient) programSize() (rows, cols string) {
	c.t.Helper()
	c.send(map[string]any{"type": "input", "data": "stty size\r"})
	var sb strings.Builder
	_ = c.conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		mt, data, err := c.conn.ReadMessage()
		if err != nil {
			return "?", "?"
		}
		if mt == websocket.TextMessage && len(data) > 0 && data[0] == '{' {
			var a sizeNotice
			if json.Unmarshal(data, &a) == nil && a.Type == "size" {
				c.mu.Lock()
				c.warnings = append(c.warnings, a)
				c.mu.Unlock()
			}
			continue
		}
		sb.Write(data)
		if m := reProgramSize.FindStringSubmatch(stripANSI(strings.ReplaceAll(sb.String(), "\r", "\n"))); m != nil {
			return m[1], m[2]
		}
	}
}

func (c *testClient) receivedNotices() []sizeNotice {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]sizeNotice(nil), c.warnings...)
}

func testSession(t *testing.T, name string) func() *testClient {
	t.Helper()
	if _, err := exec.LookPath("dtach"); err != nil {
		t.Skip("no dtach on this machine")
	}
	dir := t.TempDir()
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
		srv.Close()
		_ = exec.Command("pkill", "-f", socketPathFor(dir, name)).Run()
	})
	return func() *testClient {
		u := "ws" + strings.TrimPrefix(srv.URL, "http") + "/?size=1&name=" + name
		conn, _, err := websocket.DefaultDialer.Dial(u, nil)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		return &testClient{conn: conn, t: t}
	}
}

func TestE2E_SessionNotStuckAtDepartedClientSize(t *testing.T) {
	dial := testSession(t, "e2e-stuck")

	big := dial()
	big.resize(120, 40)
	time.Sleep(1600 * time.Millisecond)
	if r, c := big.programSize(); r != "40" || c != "120" {
		t.Fatalf("with a single client, the program sees %sx%s; wanted 40x120", r, c)
	}

	small := dial()
	small.resize(80, 24)
	time.Sleep(1600 * time.Millisecond)
	if r, c := big.programSize(); r != "24" || c != "80" {
		t.Fatalf("with both attached, the program sees %sx%s; wanted the SMALLER one, 24x80", r, c)
	}

	big.resize(110, 38)
	time.Sleep(1600 * time.Millisecond)
	if r, c := big.programSize(); r != "24" || c != "80" {
		t.Fatalf("the smaller one still rules: program sees %sx%s; wanted 24x80", r, c)
	}

	_ = small.conn.Close()
	time.Sleep(2500 * time.Millisecond)
	if r, c := big.programSize(); r != "38" || c != "110" {
		t.Errorf("after the small client left, the program sees %sx%s; wanted 38x110 — the session stayed stuck at the size of whoever closed", r, c)
	}
}

func TestE2E_LargeClientNotifiedWhenSmallJoinsAndLeaves(t *testing.T) {
	dial := testSession(t, "e2e-notices")

	big := dial()
	big.resize(120, 40)
	time.Sleep(1500 * time.Millisecond)

	small := dial()
	small.resize(80, 24)
	time.Sleep(1500 * time.Millisecond)
	big.programSize()

	warnings := big.receivedNotices()
	if len(warnings) == 0 {
		t.Fatal("the large client was not notified of the session grid when the small one joined")
	}
	if u := warnings[len(warnings)-1]; u.Cols != 80 || u.Rows != 24 {
		t.Errorf("last notice on entry: %dx%d; wanted 80x24", u.Cols, u.Rows)
	}

	_ = small.conn.Close()
	time.Sleep(2500 * time.Millisecond)
	big.programSize()

	warnings = big.receivedNotices()
	if u := warnings[len(warnings)-1]; u.Cols != 120 || u.Rows != 40 {
		t.Errorf("after the small one left, the last notice was %dx%d; wanted 120x40 — without this re-notice, the large client keeps drawing the grid of what has already left", u.Cols, u.Rows)
	}
}

func TestE2E_NewcomerIsSizedWithoutWobble(t *testing.T) {
	dial := testSession(t, "e2e-arrival")

	first := dial()
	first.resize(90, 28)
	time.Sleep(1500 * time.Millisecond)

	second := dial()
	second.resize(90, 28)
	time.Sleep(1500 * time.Millisecond)
	if r, c := second.programSize(); r != "28" || c != "90" {
		t.Errorf("the second client sees %sx%s; wanted 28x90", r, c)
	}

	if len(second.receivedNotices()) == 0 {
		t.Error("whoever joins must receive the session grid before the first byte")
	}
}
