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

// THESE TESTS ASK THE PROGRAM, NOT OUR OWN BOOKKEEPING.
//
// The defect survived a battery of green unit tests — including one called
// "a client that leaves stops shrinking the session", which measured the RETURN
// of `esqueceTamanho` while the caller threw that return away. The bookkeeping
// was right and the terminal was wrong.
//
// So here a real `HostShell` comes up, with real websockets, and the question is
// put to the session's shell: `stty size`. It is the only answer that cannot
// agree with us and be wrong at the same time.

var reTamanhoDoPrograma = regexp.MustCompile(`(?m)^\s*(\d{1,3}) (\d{1,3})\s*$`)

type clienteDeTeste struct {
	conn *websocket.Conn
	t    *testing.T

	mu     sync.Mutex
	avisos []avisoDeTamanho
}

func (c *clienteDeTeste) manda(v any) {
	c.t.Helper()
	b, _ := json.Marshal(v)
	if err := c.conn.WriteMessage(websocket.TextMessage, b); err != nil {
		c.t.Fatalf("write: %v", err)
	}
}

func (c *clienteDeTeste) redimensiona(cols, rows int) {
	c.manda(map[string]any{"type": "resize", "cols": cols, "rows": rows})
}

// tamanhoDoPrograma asks the session's shell what its terminal size is —
// `rows cols`, the way `stty size` answers.
func (c *clienteDeTeste) tamanhoDoPrograma() (rows, cols string) {
	c.t.Helper()
	c.manda(map[string]any{"type": "input", "data": "stty size\r"})
	var sb strings.Builder
	_ = c.conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		mt, data, err := c.conn.ReadMessage()
		if err != nil {
			return "?", "?"
		}
		if mt == websocket.TextMessage && len(data) > 0 && data[0] == '{' {
			var a avisoDeTamanho
			if json.Unmarshal(data, &a) == nil && a.Type == "size" {
				c.mu.Lock()
				c.avisos = append(c.avisos, a)
				c.mu.Unlock()
			}
			continue
		}
		sb.Write(data)
		if m := reTamanhoDoPrograma.FindStringSubmatch(stripANSI(strings.ReplaceAll(sb.String(), "\r", "\n"))); m != nil {
			return m[1], m[2]
		}
	}
}

func (c *clienteDeTeste) avisosRecebidos() []avisoDeTamanho {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]avisoDeTamanho(nil), c.avisos...)
}

// sessaoDeTeste brings up a real HostShell and returns how to dial into it.
func sessaoDeTeste(t *testing.T, nome string) func() *clienteDeTeste {
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
		_ = exec.Command("pkill", "-f", socketPathFor(dir, nome)).Run()
	})
	return func() *clienteDeTeste {
		u := "ws" + strings.TrimPrefix(srv.URL, "http") + "/?size=1&name=" + nome
		conn, _, err := websocket.DefaultDialer.Dial(u, nil)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		return &clienteDeTeste{conn: conn, t: t}
	}
}

// THE REPORT, IN FULL: two PCs on the same session, the one with the smaller
// window closes, and the one left has to go back to its own size.
//
// Before the fix the program kept painting 24x80 while the browser drew 38x110 —
// forever, because the per-connection dedup blocked the heartbeat's re-assertion
// before it reached the session. That is where both complaints came from at once:
// "I only see one page" (the program paints fewer lines than the window shows)
// and "the text duplicates" (it wraps lines at one width while the xterm draws
// at another).
func TestE2E_SessaoNaoFicaPresaNoTamanhoDoClienteQueSaiu(t *testing.T) {
	disca := sessaoDeTeste(t, "e2e-preso")

	grande := disca() // o PC novo
	grande.redimensiona(120, 40)
	time.Sleep(1600 * time.Millisecond)
	if r, c := grande.tamanhoDoPrograma(); r != "40" || c != "120" {
		t.Fatalf("with a single client, the program sees %sx%s; wanted 40x120", r, c)
	}

	pequeno := disca() // a aba esquecida aberta no PC antigo
	pequeno.redimensiona(80, 24)
	time.Sleep(1600 * time.Millisecond)
	if r, c := grande.tamanhoDoPrograma(); r != "24" || c != "80" {
		t.Fatalf("with both attached, the program sees %sx%s; wanted the SMALLER one, 24x80", r, c)
	}

	// The new PC touches its window while both are attached. It is this step that
	// put the big client's pty at the minimum and sprang the trap.
	grande.redimensiona(110, 38)
	time.Sleep(1600 * time.Millisecond)
	if r, c := grande.tamanhoDoPrograma(); r != "24" || c != "80" {
		t.Fatalf("the smaller one still rules: program sees %sx%s; wanted 24x80", r, c)
	}

	_ = pequeno.conn.Close() // close the tab on the old PC
	time.Sleep(2500 * time.Millisecond)
	if r, c := grande.tamanhoDoPrograma(); r != "38" || c != "110" {
		t.Errorf("after the small client left, the program sees %sx%s; wanted 38x110 — the session stayed stuck at the size of whoever closed", r, c)
	}
}

// The other half of the minimum rule: whoever ends up LARGER needs to be told the
// session's grid, and told again when it changes. Without the re-notice, the big
// client draws a grid the program is not painting for.
func TestE2E_ClienteGrandeEAvisadoNaEntradaENaSaidaDoPequeno(t *testing.T) {
	disca := sessaoDeTeste(t, "e2e-avisos")

	grande := disca()
	grande.redimensiona(120, 40)
	time.Sleep(1500 * time.Millisecond)

	pequeno := disca()
	pequeno.redimensiona(80, 24)
	time.Sleep(1500 * time.Millisecond)
	grande.tamanhoDoPrograma() // drena o socket, coletando avisos

	avisos := grande.avisosRecebidos()
	if len(avisos) == 0 {
		t.Fatal("the large client was not notified of the session grid when the small one joined")
	}
	if u := avisos[len(avisos)-1]; u.Cols != 80 || u.Rows != 24 {
		t.Errorf("last notice on entry: %dx%d; wanted 80x24", u.Cols, u.Rows)
	}

	_ = pequeno.conn.Close()
	time.Sleep(2500 * time.Millisecond)
	grande.tamanhoDoPrograma()

	avisos = grande.avisosRecebidos()
	if u := avisos[len(avisos)-1]; u.Cols != 120 || u.Rows != 40 {
		t.Errorf("after the small one left, the last notice was %dx%d; wanted 120x40 — without this re-notice, the large client keeps drawing the grid of what has already left", u.Cols, u.Rows)
	}
}

// A client arriving mid-session gets ITS pty sized even when the session's size
// does not change. `pty.Start` creates the pty at 0x0; before, the only path that
// fixed that was the repaint-wobble, by accident — and the wobble leaves the
// stage for a client that primes its own screen (`replay=0`).
func TestE2E_QuemChegaEDimensionadoSemDependerDoWobble(t *testing.T) {
	disca := sessaoDeTeste(t, "e2e-chegada")

	primeiro := disca()
	primeiro.redimensiona(90, 28)
	time.Sleep(1500 * time.Millisecond)

	segundo := disca()
	segundo.redimensiona(90, 28) // same size: the session does NOT change
	time.Sleep(1500 * time.Millisecond)
	if r, c := segundo.tamanhoDoPrograma(); r != "28" || c != "90" {
		t.Errorf("the second client sees %sx%s; wanted 28x90", r, c)
	}

	if len(segundo.avisosRecebidos()) == 0 {
		t.Error("whoever joins must receive the session grid before the first byte")
	}
}
