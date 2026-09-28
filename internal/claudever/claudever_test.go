package claudever

import "testing"

func TestOnlyOutdatedNeedRestart(t *testing.T) {
	cases := []struct {
		version, installed string
		behind             bool
		why                string
	}{
		{"2.1.238", "2.1.240", true, "patch behind"},
		{"2.1.240", "2.1.240", false, "same version"},
		{"2.1.241", "2.1.240", false, "AHEAD (container with its own installation)"},
		{"2.0.999", "2.1.0", true, "minor behind despite a higher patch"},
		{"2.1.9", "2.1.10", true, "10 > 9: numeric comparison, not alphabetical"},
		{"2.1.10", "2.1.9", false, "ahead for the same reason"},
		{"3.0.0", "2.9.9", false, "major ahead"},
		{"", "2.1.240", false, "no version: cannot claim it is behind"},
		{"2.1.240", "", false, "no reference: same"},
	}
	for _, c := range cases {
		if got := isOlder(c.version, c.installed); got != c.behind {
			t.Errorf("isOlder(%q, %q) = %v, wanted %v — %s", c.version, c.installed, got, c.behind, c.why)
		}
	}
}

func TestVersionComesFromBinaryPath(t *testing.T) {
	cases := map[string]string{
		"/root/.local/share/claude/versions/2.1.240": "2.1.240",
		"/opt/x/claude/versions/2.1.9":               "2.1.9",
		"/usr/bin/bash":                              "",
		"/root/.local/share/claude/versions/nightly": "",
		"":                  "",
		"/claude/versions/": "",
	}
	for path, expected := range cases {
		if got := versionFromPath(path); got != expected {
			t.Errorf("versionFromPath(%q) = %q, wanted %q", path, got, expected)
		}
	}
}

func TestParentOfHandlesProcessNameWithSpace(t *testing.T) {
	dir := t.TempDir()
	prevRoot := procRoot
	procRoot = dir
	defer func() { procRoot = prevRoot }()

	writeOut := func(pid, ppid int, name string) {
		d := dir + "/" + itoa(pid)
		if err := mkdirAll(d); err != nil {
			t.Fatal(err)
		}
		line := itoa(pid) + " (" + name + ") S " + itoa(ppid) + " 1 1 0 -1 4194304 100 0 0 0"
		if err := writeFile(d+"/stat", line); err != nil {
			t.Fatal(err)
		}
	}

	writeOut(100, 42, "claude")
	writeOut(101, 43, "meu app (v2)")
	writeOut(102, 44, "a b c")

	for _, c := range []struct{ pid, ppid int }{{100, 42}, {101, 43}, {102, 44}} {
		if got := ParentOf(c.pid); got != c.ppid {
			t.Errorf("ParentOf(%d) = %d, wanted %d", c.pid, got, c.ppid)
		}
	}
}

func TestAncestorDoesNotLoop(t *testing.T) {
	dir := t.TempDir()
	prevRoot := procRoot
	procRoot = dir
	defer func() { procRoot = prevRoot }()

	for _, c := range []struct{ pid, ppid int }{{200, 201}, {201, 200}} {
		d := dir + "/" + itoa(c.pid)
		if err := mkdirAll(d); err != nil {
			t.Fatal(err)
		}
		if err := writeFile(d+"/stat", itoa(c.pid)+" (x) S "+itoa(c.ppid)+" 1"); err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan int, 1)
	go func() { done <- AncestorIn(200, map[int]bool{999: true}) }()
	select {
	case got := <-done:
		if got != 0 {
			t.Errorf("found ancestor %d where there was none", got)
		}
	case <-shortTimeout():
		t.Fatal("AncestorIn did not terminate — an infinite loop with /proc in a cycle")
	}
}
