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

// THE SIZE RULE, IN UNIT FORM.
//
// The minimum did not disappear — it became a ceiling for clients that do not
// accept frames. With nobody who accepts them, this has to degenerate into
// exactly the old rule, which is what makes the change safe for a client that has
// not been updated yet.
func TestTamanhoDaSessaoFicaNoMaiorEntreQuemAceitaQuadro(t *testing.T) {
	casos := []struct {
		nome       string
		clientes   []tamanhoDoCliente
		querCols   uint16
		querRows   uint16
		porqueDiso string
	}{
		{
			"desktop e celular, os dois aceitam quadro",
			[]tamanhoDoCliente{{cols: 120, rows: 40, aceitaQuadro: true}, {cols: 53, rows: 20, aceitaQuadro: true}},
			120, 40,
			"o celular não pode mais encolher o desktop — ele recebe recorte",
		},
		{
			"ninguém aceita quadro: a regra antiga, intacta",
			[]tamanhoDoCliente{{cols: 120, rows: 40}, {cols: 53, rows: 20}},
			53, 20,
			"cliente antigo só sabe desenhar o fluxo cru, então a sessão cabe nele",
		},
		{
			"um aceita, outro não: quem não aceita é TETO",
			[]tamanhoDoCliente{{cols: 120, rows: 40, aceitaQuadro: true}, {cols: 80, rows: 24}},
			80, 24,
			"o antigo desenharia lixo se a sessão passasse do tamanho dele",
		},
		{
			"um cliente só",
			[]tamanhoDoCliente{{cols: 100, rows: 30, aceitaQuadro: true}},
			100, 30,
			"sem disputa, a sessão é a janela dele",
		},
	}
	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			s := &logCompartilhado{}
			var cols, rows uint16
			for i, c := range caso.clientes {
				cols, rows, _, _ = s.registraTamanho(int64(i+1), c.cols, c.rows, c.aceitaQuadro)
			}
			if cols != caso.querCols || rows != caso.querRows {
				t.Errorf("session ended up %dx%d; wanted %dx%d — %s",
					cols, rows, caso.querCols, caso.querRows, caso.porqueDiso)
			}
		})
	}
}

// clienteQuadro is a test client that accepts a rendered crop.
type clienteQuadro struct {
	conn *websocket.Conn
	t    *testing.T

	mu      sync.Mutex
	recebeu []byte
	avisos  []avisoDeTamanho
}

func (c *clienteQuadro) escuta() {
	for {
		mt, data, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		c.mu.Lock()
		if mt == websocket.TextMessage && len(data) > 0 && data[0] == '{' {
			var a avisoDeTamanho
			if json.Unmarshal(data, &a) == nil && a.Type == "size" {
				c.avisos = append(c.avisos, a)
			}
		} else {
			c.recebeu = append(c.recebeu, data...)
		}
		c.mu.Unlock()
	}
}

func (c *clienteQuadro) texto() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return stripANSI(string(c.recebeu))
}

func (c *clienteQuadro) avisosRecebidos() []avisoDeTamanho {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]avisoDeTamanho(nil), c.avisos...)
}

func (c *clienteQuadro) manda(v any) {
	b, _ := json.Marshal(v)
	_ = c.conn.WriteMessage(websocket.TextMessage, b)
}

// THE REPORT THIS TEST CLOSES: "the phone still shrinks the desktop".
//
// Two clients on the same session, both accepting frames. The program has to see
// the LARGER one's window, and the smaller one has to keep seeing the session —
// through a rendered crop, not through the raw stream, which at that width would
// land entirely in the wrong place.
func TestE2EQuadro_CelularNaoEncolheMaisODesktop(t *testing.T) {
	if _, err := exec.LookPath("dtach"); err != nil {
		t.Skip("no dtach on this machine")
	}
	dir := t.TempDir()
	nome := "quadro-e2e"
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
		PararGravador(dir, "u", nome)
		_ = exec.Command("pkill", "-f", socketPathFor(dir, nome)).Run()
	})

	disca := func(extra string) *clienteQuadro {
		u := "ws" + strings.TrimPrefix(srv.URL, "http") + "/?size=1&name=" + nome + extra
		conn, _, err := websocket.DefaultDialer.Dial(u, nil)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		c := &clienteQuadro{conn: conn, t: t}
		// Closing at the end of the test matters: the registry of live connections
		// (`registerLive`, used by the restart notice) belongs to the PACKAGE, and a
		// connection left open here shows up in another test's count.
		t.Cleanup(func() { _ = conn.Close() })
		go c.escuta()
		return c
	}

	// The desktop arrives first, big, and accepts frames.
	desktop := disca("&quadro=1")
	desktop.manda(map[string]any{"type": "resize", "cols": 120, "rows": 40})
	time.Sleep(1800 * time.Millisecond)

	// The phone arrives later, small, and accepts frames too.
	celular := disca("&quadro=1")
	celular.manda(map[string]any{"type": "resize", "cols": 53, "rows": 20})
	time.Sleep(2000 * time.Millisecond)

	// The question is put to the PROGRAM, not to our own bookkeeping.
	desktop.manda(map[string]any{"type": "input", "data": "stty size\r"})
	time.Sleep(1500 * time.Millisecond)

	m := reTamanhoDoPrograma.FindStringSubmatch(strings.ReplaceAll(desktop.texto(), "\r", "\n"))
	if m == nil {
		t.Fatalf("could not read the program's size; the desktop received: %q", desktop.texto())
	}
	if m[1] != "40" || m[2] != "120" {
		t.Errorf("with the phone attached the program sees %sx%s; wanted 40x120 — the phone went back to shrinking the desktop", m[1], m[2])
	}

	// AND THE PHONE KEEPS SEEING THE SESSION. This is the other half: it is no use
	// for the desktop to grow if the smaller one turns into a dead screen. What
	// reaches it is the RENDERED crop — the raw stream, at that width, would land
	// entirely in the wrong place.
	desktop.manda(map[string]any{"type": "input", "data": "echo MARCA_QUADRO\r"})
	time.Sleep(2 * time.Second)
	if txt := celular.texto(); !strings.Contains(txt, "MARCA_QUADRO") {
		t.Errorf("the phone did not see what was typed on the desktop; it received %d bytes: %.300q",
			len(txt), txt)
	}

	// The phone must NOT have been told to draw the session's grid: it draws ITS
	// OWN WINDOW, and the server composes the crop. The notice exists and carries
	// its size — that is how a client already told the session's grid leaves it
	// when it shrinks.
	avisos := celular.avisosRecebidos()
	if len(avisos) == 0 {
		t.Error("the phone received no notice at all — it would get stuck on the last grid it knew")
	}
	for _, a := range avisos {
		if a.Cols > 53 {
			t.Errorf("the phone received a notice to draw %dx%d — in frame mode it draws its own window", a.Cols, a.Rows)
		}
	}
}
