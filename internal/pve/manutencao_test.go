package pve

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestRebootMontaARotaCerta — reboot is a /status verb like the others, and the
// pin exists so that it does NOT turn into stop+start in a distracted
// refactoring: the two look alike on the screen and are very different for
// whoever is inside the guest.
func TestRebootMontaARotaCerta(t *testing.T) {
	var visto string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		visto = r.URL.Path
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		_, _ = w.Write([]byte(`{"data":"` + upidFalso + `"}`))
	})
	if _, err := c.Reboot(context.Background(), "pve", 207, "lxc"); err != nil {
		t.Fatal(err)
	}
	if quer := "/api2/json/nodes/pve/lxc/207/status/reboot"; visto != quer {
		t.Errorf("route = %q, want %q", visto, quer)
	}
}

// 🔴 TestCloneUsaOParametroDeNomeCERTOPorTipo — the defect this pin exists to
// prevent is SILENT: LXC uses `hostname`, QEMU uses `name`, and sending the
// wrong one raises no error. The hypervisor simply ignores it, and the clone is
// born without a name. It only turns up days later, looking at a list with an
// anonymous guest in it.
func TestCloneUsaOParametroDeNomeCERTOPorTipo(t *testing.T) {
	casos := []struct {
		typ, esperaChave, naoEspera string
	}{
		{"lxc", "hostname", "name"},
		{"qemu", "name", "hostname"},
	}
	for _, cs := range casos {
		var q url.Values
		var caminho string
		c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			q = r.URL.Query()
			caminho = r.URL.Path
			_, _ = w.Write([]byte(`{"data":"` + upidFalso + `"}`))
		})
		if _, err := c.Clone(context.Background(), "pve", 204, cs.typ, 991, "copia-de-lab", ""); err != nil {
			t.Fatalf("%s: %v", cs.typ, err)
		}
		if q.Get(cs.esperaChave) != "copia-de-lab" {
			t.Errorf("%s: %s = %q, want the name", cs.typ, cs.esperaChave, q.Get(cs.esperaChave))
		}
		if q.Has(cs.naoEspera) {
			t.Errorf("%s: it sent %q, which this type IGNORES — the clone would be born with no name", cs.typ, cs.naoEspera)
		}
		if q.Get("newid") != "991" {
			t.Errorf("%s: newid = %q", cs.typ, q.Get("newid"))
		}
		// full=1 always: a normal guest does not accept a linked clone.
		if q.Get("full") != "1" {
			t.Errorf("%s: full = %q, want 1 — a guest that is not a template only accepts a full copy", cs.typ, q.Get("full"))
		}
		if quer := "/api2/json/nodes/pve/" + cs.typ + "/204/clone"; caminho != quer {
			t.Errorf("%s: route = %q, want %q", cs.typ, caminho, quer)
		}
	}
}

// TestCloneRecusaAntesDeDiscar — a destination equal to the source, an invalid
// id and a name that escapes the parameter all have to die HERE. A clone with
// the wrong destination has no cheap undo: either a guest nobody asked for is
// born, or the POST turns into another route.
func TestCloneRecusaAntesDeDiscar(t *testing.T) {
	casos := []struct {
		nome          string
		novoID        int
		nomeDoDestino string
	}{
		{"destino igual à origem", 204, "ok"},
		{"id zero", 0, "ok"},
		{"id negativo", -1, "ok"},
		{"nome com barra", 991, "../../status/stop"},
		{"nome com espaço", 991, "com espaço"},
		{"nome com acento", 991, "cópia"},
		{"nome começando com hífen", 991, "-copia"},
		{"nome terminando com hífen", 991, "copia-"},
		{"nome longo demais", 991, strings.Repeat("a", 64)},
	}
	for _, cs := range casos {
		var discou bool
		c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			discou = true
			_, _ = w.Write([]byte(`{"data":"` + upidFalso + `"}`))
		})
		if _, err := c.Clone(context.Background(), "pve", 204, "lxc", cs.novoID, cs.nomeDoDestino, ""); err == nil {
			t.Errorf("%s: it was accepted", cs.nome)
		}
		if discou {
			t.Errorf("%s: IT ACTUALLY DIALED — it has to be refused before that", cs.nome)
		}
	}
}

// TestVZDumpNaoPodaNemPedePrivilegioExtra — the pin that guards the most
// important decision of this route. `prune-backups` would make a command the
// operator presses to GAIN a copy end up DELETING others;
// `bwlimit`/`ionice`/`performance` would require Sys.Modify on '/'. None of them
// may show up in the query.
func TestVZDumpNaoPodaNemPedePrivilegioExtra(t *testing.T) {
	var q url.Values
	var caminho string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		q = r.URL.Query()
		caminho = r.URL.Path
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		_, _ = w.Write([]byte(`{"data":"` + upidFalso + `"}`))
	})
	if _, err := c.VZDump(context.Background(), "pve", 204, "pbs", "snapshot", "zstd"); err != nil {
		t.Fatal(err)
	}
	if quer := "/api2/json/nodes/pve/vzdump"; caminho != quer {
		t.Errorf("route = %q, want %q", caminho, quer)
	}
	for _, proibido := range []string{"prune-backups", "bwlimit", "ionice", "performance", "dumpdir", "tmpdir", "script", "job-id"} {
		if q.Has(proibido) {
			t.Errorf("the query carried %q — it either deletes someone else's backup, or demands a privilege nobody needs to have", proibido)
		}
	}
	if q.Get("remove") != "0" {
		t.Errorf("remove = %q, want \"0\" — a manual backup deletes nothing", q.Get("remove"))
	}
	for k, quer := range map[string]string{"vmid": "204", "storage": "pbs", "mode": "snapshot", "compress": "zstd"} {
		if q.Get(k) != quer {
			t.Errorf("%s = %q, want %q", k, q.Get(k), quer)
		}
	}
}

// TestVZDumpRecusaForaDaAllowlist — mode, compression and storage go into the
// body of a POST to the hypervisor. A free string would give the panel the
// chance to send anything a future hypervisor version might come to accept there.
func TestVZDumpRecusaForaDaAllowlist(t *testing.T) {
	casos := []struct{ nome, storage, modo, compress string }{
		{"modo inventado", "pbs", "rapido", "zstd"},
		{"modo com espaço", "pbs", "snapshot ", "zstd"},
		{"modo em maiúscula", "pbs", "SNAPSHOT", "zstd"},
		{"compressão inventada", "pbs", "snapshot", "brotli"},
		{"storage com barra", "../../vms/100", "snapshot", "zstd"},
		{"storage com ponto-ponto", "a..b", "snapshot", "zstd"},
		{"storage vazio", "", "snapshot", "zstd"},
		{"nó com barra", "pbs", "snapshot", "zstd"},
	}
	for i, cs := range casos {
		var discou bool
		c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			discou = true
			_, _ = w.Write([]byte(`{"data":"` + upidFalso + `"}`))
		})
		no := "pve"
		if i == len(casos)-1 {
			no = "../../cluster"
		}
		if _, err := c.VZDump(context.Background(), no, 204, cs.storage, cs.modo, cs.compress); err == nil {
			t.Errorf("%s: it was accepted", cs.nome)
		}
		if discou {
			t.Errorf("%s: IT ACTUALLY DIALED", cs.nome)
		}
	}
}

// TestNextIDLeStringENumero — /cluster/nextid returns the number as a JSON
// STRING. A parser that only accepts a number returns zero in silence, and zero
// becomes "invalid vmid" further down the line, far from the cause.
func TestNextIDLeStringENumero(t *testing.T) {
	for _, corpo := range []string{`{"data":"991"}`, `{"data":991}`} {
		c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api2/json/cluster/nextid" {
				t.Errorf("route = %q", r.URL.Path)
			}
			_, _ = w.Write([]byte(corpo))
		})
		n, err := c.NextID(context.Background())
		if err != nil {
			t.Fatalf("%s: %v", corpo, err)
		}
		if n != 991 {
			t.Errorf("%s: NextID = %d, want 991", corpo, n)
		}
	}
}

// 🔴 TestWaitTaskSeparaAvisoDeFalha — the distinction that the live proof of the
// clone forced into existence.
//
// The clone transferred 768 MB, created the guest and finished in `WARNINGS: 1`,
// with a single warning: "Systemd 257 detected. You may need to enable
// nesting.". Treating that as a failure would tell the operator the clone did
// not happen — and it did. Swallowing it would be worse: the warning is exactly
// what nobody else reads.
//
// The pin proves BOTH directions, because loosening this in the wrong direction
// would turn a real failure into a silent success.
func TestWaitTaskSeparaAvisoDeFalha(t *testing.T) {
	casos := []struct {
		exit      string
		querAviso bool
		querErro  bool
	}{
		{"OK", false, false},
		{"WARNINGS: 1", true, false},
		{"WARNINGS: 12", true, false},
		{"unable to create CT 991 - storage full", false, true},
		{"", false, true},
		{"command 'lxc-start' failed: exit code 1", false, true},
		// 🔴 The negative control that matters: a failure that MENTIONS the word
		// cannot become a warning. Only an exitstatus that BEGINS with WARNINGS: counts.
		{"failed with WARNINGS: something", false, true},
	}
	for _, cs := range casos {
		c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			b, _ := json.Marshal(map[string]any{"data": map[string]any{
				"status": "stopped", "exitstatus": cs.exit,
			}})
			_, _ = w.Write(b)
		})
		c.taskPoll = time.Millisecond
		err := c.WaitTask(context.Background(), "pve", upidFalso)
		aviso, temAviso := TarefaComAvisos(err)

		if temAviso != cs.querAviso {
			t.Errorf("exitstatus %q: warning = %v, want %v", cs.exit, temAviso, cs.querAviso)
		}
		if cs.querAviso {
			if aviso.Exit != cs.exit {
				t.Errorf("exitstatus %q: the warning lost its text (%q) — whoever does not read it here reads it nowhere", cs.exit, aviso.Exit)
			}
			continue
		}
		if cs.querErro && err == nil {
			t.Errorf("exitstatus %q: WaitTask returned nil — a failure turned into success", cs.exit)
		}
		if !cs.querErro && err != nil {
			t.Errorf("exitstatus %q: WaitTask returned an error (%v)", cs.exit, err)
		}
	}
}
