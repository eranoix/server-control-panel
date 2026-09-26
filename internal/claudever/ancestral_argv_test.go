package claudever

import (
	"testing"
	"time"
)

// buildProc writes a fake /proc: each entry is (pid, ppid, cmdline).
func buildProc(t *testing.T, ents ...struct {
	pid, ppid int
	cmd       string
}) {
	t.Helper()
	dir := t.TempDir()
	anterior := procRoot
	procRoot = dir
	t.Cleanup(func() { procRoot = anterior })

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
// resolution by PID (AncestorIn) returns zero for everything and the panel's
// "Restart" button is born disabled on every row. The anchor that works is the
// socket path in the master's argv. Tree identical to production's:
//
//	claude  ->  bash -l  ->  dtach -n /…/session-sox/Server.sock -E -z bash -l
func TestAncestorByArgvFindsSessionViaMasterSocket(t *testing.T) {
	buildProc(t,
		ent{300, 301, "claude --continue"},
		ent{301, 302, "/usr/bin/bash -l"},
		ent{302, 1, "/usr/bin/dtach -n /opt/panel/data/session-sox/Server.sock -E -z /usr/bin/bash -l"},
	)
	marks := map[string]string{
		"/opt/panel/data/session-sox/Server.sock": "Server",
		"/opt/panel/data/session-sox/Css.sock":    "Css",
	}
	if got := AncestorByArgv(300, marks); got != "Server" {
		t.Fatalf("AncestorByArgv = %q, want \"Server\"", got)
	}
}

// A process outside any of the backend's sessions (e.g. a stray multiplexer)
// still has no owner — inventing one here would make the panel enable a button
// that would restart the WRONG session.
func TestAncestorByArgvDoesNotInventOwner(t *testing.T) {
	buildProc(t,
		ent{400, 401, "claude --continue"},
		ent{401, 1, "other-mux new-session -d -s claude-rc claude --continue"},
	)
	marks := map[string]string{"/opt/panel/data/session-sox/Server.sock": "Server"}
	if got := AncestorByArgv(400, marks); got != "" {
		t.Fatalf("AncestorByArgv = %q, wanted empty", got)
	}
}

func TestAncestorByArgvNoMarks(t *testing.T) {
	buildProc(t, ent{500, 1, "claude"})
	if got := AncestorByArgv(500, nil); got != "" {
		t.Fatalf("AncestorByArgv(nil) = %q, wanted empty", got)
	}
}

// The pid itself can be the master (a direct spawn of `dtach -n … claude`).
func TestAncestorByArgvMatchesOwnPid(t *testing.T) {
	buildProc(t, ent{600, 1, "/usr/bin/dtach -n /opt/panel/data/session-sox/Vpsm.sock -E -z claude"})
	marks := map[string]string{"/opt/panel/data/session-sox/Vpsm.sock": "Vpsm"}
	if got := AncestorByArgv(600, marks); got != "Vpsm" {
		t.Fatalf("AncestorByArgv = %q, want \"Vpsm\"", got)
	}
}

// Same reason as TestAncestorDoesNotLoop: a recycled PID has already produced
// a cycle in this sweep, and a loop here hangs the panel's handler.
func TestAncestorByArgvDoesNotLoop(t *testing.T) {
	buildProc(t,
		ent{700, 701, "claude"},
		ent{701, 700, "bash"},
	)
	done := make(chan string, 1)
	go func() { done <- AncestorByArgv(700, map[string]string{"/no/match.sock": "x"}) }()
	select {
	case got := <-done:
		if got != "" {
			t.Fatalf("AncestorByArgv = %q, wanted empty", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("AncestorByArgv went into a loop")
	}
}
