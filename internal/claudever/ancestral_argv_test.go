package claudever

import (
	"testing"
	"time"
)

// montaProc writes a fake /proc: each entry is (pid, ppid, cmdline).
func montaProc(t *testing.T, ents ...struct {
	pid, ppid int
	cmd       string
}) {
	t.Helper()
	dir := t.TempDir()
	anterior := raizProc
	raizProc = dir
	t.Cleanup(func() { raizProc = anterior })

	for _, e := range ents {
		d := dir + "/" + itoa(e.pid)
		if err := mkdirAll(d); err != nil {
			t.Fatal(err)
		}
		if err := writeFile(d+"/stat", itoa(e.pid)+" (claude) S "+itoa(e.ppid)+" 1"); err != nil {
			t.Fatal(err)
		}
		// a real cmdline is NUL-separated; the trailing \x00 imitates the format.
		if err := writeFile(d+"/cmdline", e.cmd+"\x00"); err != nil {
			t.Fatal(err)
		}
	}
}

type ent = struct {
	pid, ppid int
	cmd       string
}

// The regression: with the dtach backend the record does NOT keep a PID, so
// resolution by PID (AncestralEm) returns zero for everything and the panel's
// "Restart" button is born disabled on every row. The anchor that works is the
// socket path in the master's argv. Tree identical to production's:
//
//	claude  ->  bash -l  ->  dtach -n /…/session-sox/Servidor.sock -E -z bash -l
func TestAncestralPorArgvAchaSessaoPeloSocketDoMaster(t *testing.T) {
	montaProc(t,
		ent{300, 301, "claude --continue"},
		ent{301, 302, "/usr/bin/bash -l"},
		ent{302, 1, "/usr/bin/dtach -n /opt/panel/data/session-sox/Servidor.sock -E -z /usr/bin/bash -l"},
	)
	marcas := map[string]string{
		"/opt/panel/data/session-sox/Servidor.sock": "Servidor",
		"/opt/panel/data/session-sox/Css.sock":      "Css",
	}
	if got := AncestralPorArgv(300, marcas); got != "Servidor" {
		t.Fatalf("AncestralPorArgv = %q, want \"Servidor\"", got)
	}
}

// A process outside any of the backend's sessions (e.g. a stray multiplexer)
// still has no owner — inventing one here would make the panel enable a button
// that would restart the WRONG session.
func TestAncestralPorArgvNaoInventaDono(t *testing.T) {
	montaProc(t,
		ent{400, 401, "claude --continue"},
		ent{401, 1, "outro-mux new-session -d -s claude-rc claude --continue"},
	)
	marcas := map[string]string{"/opt/panel/data/session-sox/Servidor.sock": "Servidor"}
	if got := AncestralPorArgv(400, marcas); got != "" {
		t.Fatalf("AncestralPorArgv = %q, wanted empty", got)
	}
}

func TestAncestralPorArgvSemMarcas(t *testing.T) {
	montaProc(t, ent{500, 1, "claude"})
	if got := AncestralPorArgv(500, nil); got != "" {
		t.Fatalf("AncestralPorArgv(nil) = %q, wanted empty", got)
	}
}

// The pid itself can be the master (a direct spawn of `dtach -n … claude`).
func TestAncestralPorArgvCasaNoProprioPid(t *testing.T) {
	montaProc(t, ent{600, 1, "/usr/bin/dtach -n /opt/panel/data/session-sox/Vpsm.sock -E -z claude"})
	marcas := map[string]string{"/opt/panel/data/session-sox/Vpsm.sock": "Vpsm"}
	if got := AncestralPorArgv(600, marcas); got != "Vpsm" {
		t.Fatalf("AncestralPorArgv = %q, want \"Vpsm\"", got)
	}
}

// Same reason as TestAncestralNaoEntraEmLaco: a recycled PID has already produced
// a cycle in this sweep, and a loop here hangs the panel's handler.
func TestAncestralPorArgvNaoEntraEmLaco(t *testing.T) {
	montaProc(t,
		ent{700, 701, "claude"},
		ent{701, 700, "bash"},
	)
	feito := make(chan string, 1)
	go func() { feito <- AncestralPorArgv(700, map[string]string{"/nao/casa.sock": "x"}) }()
	select {
	case got := <-feito:
		if got != "" {
			t.Fatalf("AncestralPorArgv = %q, wanted empty", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("AncestralPorArgv went into a loop")
	}
}
