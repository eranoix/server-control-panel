package pty

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestSessionSizeIsLargestAmongFrameClients(t *testing.T) {
	cases := []struct {
		name     string
		clients  []clientSize
		wantCols uint16
		wantRows uint16
		why      string
	}{
		{
			"desktop and phone, both accept frames",
			[]clientSize{{cols: 120, rows: 40, acceptsFrame: true}, {cols: 53, rows: 20, acceptsFrame: true}},
			120, 40,
			"the phone can no longer shrink the desktop: it gets a crop",
		},
		{
			"nobody accepts frames: the old rule, intact",
			[]clientSize{{cols: 120, rows: 40}, {cols: 53, rows: 20}},
			53, 20,
			"an old client can only draw the raw stream, so the session fits it",
		},
		{
			"one accepts, the other does not: the one that does not is the CAP",
			[]clientSize{{cols: 120, rows: 40, acceptsFrame: true}, {cols: 80, rows: 24}},
			80, 24,
			"the old one would draw garbage if the session exceeded its size",
		},
		{
			"a single client",
			[]clientSize{{cols: 100, rows: 30, acceptsFrame: true}},
			100, 30,
			"no contention, the session is its window",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &sharedLog{}
			var cols, rows uint16
			for i, c := range tc.clients {
				cols, rows, _, _ = s.registerSize(int64(i+1), c.cols, c.rows, c.acceptsFrame)
			}
			if cols != tc.wantCols || rows != tc.wantRows {
				t.Errorf("session ended up %dx%d; wanted %dx%d — %s",
					cols, rows, tc.wantCols, tc.wantRows, tc.why)
			}
		})
	}
}

type frameClient struct {
	conn *websocket.Conn
	t    *testing.T

	mu       sync.Mutex
	received []byte
	warnings []sizeNotice
}

func (c *frameClient) listen() {
	for {
		mt, data, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		c.mu.Lock()
		if mt == websocket.TextMessage && len(data) > 0 && data[0] == '{' {
			var a sizeNotice
			if json.Unmarshal(data, &a) == nil && a.Type == "size" {
				c.warnings = append(c.warnings, a)
			}
		} else {
			c.received = append(c.received, data...)
		}
		c.mu.Unlock()
	}
}

func (c *frameClient) text() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return stripANSI(string(c.received))
}

func (c *frameClient) receivedNotices() []sizeNotice {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]sizeNotice(nil), c.warnings...)
}

func (c *frameClient) send(v any) {
	b, _ := json.Marshal(v)
	_ = c.conn.WriteMessage(websocket.TextMessage, b)
}

func TestE2EFrame_PhoneNoLongerShrinksDesktop(t *testing.T) {
	if _, err := exec.LookPath("dtach"); err != nil {
		t.Skip("no dtach on this machine")
	}
	dir := t.TempDir()
	name := "frame-e2e"
	own, err := LoadOwnership(dir + "/own.json")
	if err != nil {
		t.Fatal(err)
	}
	reg, err := LoadRegistry(dir + "/reg.json")
	if err != nil {
		t.Fatal(err)
	}
	InitSessionBackend(dir, reg)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		HostShell(w, r, "u", true, own, "", dir, reg)
	}))
	t.Cleanup(func() {
		srv.Close()
		StopRecorder(dir, "u", name)
		_ = exec.Command("pkill", "-f", socketPathFor(dir, name)).Run()
	})

	dial := func(extra string) *frameClient {
		u := "ws" + strings.TrimPrefix(srv.URL, "http") + "/?size=1&name=" + name + extra
		conn, _, err := websocket.DefaultDialer.Dial(u, nil)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		c := &frameClient{conn: conn, t: t}
		t.Cleanup(func() { _ = conn.Close() })
		go c.listen()
		return c
	}

	desktop := dial("&frame=1")
	desktop.send(map[string]any{"type": "resize", "cols": 120, "rows": 40})
	time.Sleep(1800 * time.Millisecond)

	phone := dial("&frame=1")
	phone.send(map[string]any{"type": "resize", "cols": 53, "rows": 20})
	time.Sleep(2000 * time.Millisecond)

	desktop.send(map[string]any{"type": "input", "data": "stty size\r"})
	time.Sleep(1500 * time.Millisecond)

	m := reProgramSize.FindStringSubmatch(strings.ReplaceAll(desktop.text(), "\r", "\n"))
	if m == nil {
		t.Fatalf("could not read the program's size; the desktop received: %q", desktop.text())
	}
	if m[1] != "40" || m[2] != "120" {
		t.Errorf("with the phone attached the program sees %sx%s; wanted 40x120 — the phone went back to shrinking the desktop", m[1], m[2])
	}

	desktop.send(map[string]any{"type": "input", "data": "echo FRAME_MARKER\r"})
	time.Sleep(2 * time.Second)
	if txt := phone.text(); !strings.Contains(txt, "FRAME_MARKER") {
		t.Errorf("the phone did not see what was typed on the desktop; it received %d bytes: %.300q",
			len(txt), txt)
	}

	warnings := phone.receivedNotices()
	if len(warnings) == 0 {
		t.Error("the phone received no notice at all — it would get stuck on the last grid it knew")
	}
	for _, a := range warnings {
		if a.Cols > 53 {
			t.Errorf("the phone received a notice to draw %dx%d — in frame mode it draws its own window", a.Cols, a.Rows)
		}
	}
}
