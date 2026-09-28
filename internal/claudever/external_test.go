package claudever

import (
	"os"
	"path/filepath"
	"testing"
)

func buildExternal(t *testing.T, pid int, exe, env, refSymlink string, sameNS bool) string {
	t.Helper()
	dir := t.TempDir()
	anterior := procRoot
	procRoot = dir
	t.Cleanup(func() { procRoot = anterior })

	self := filepath.Join(dir, "self", "ns")
	if err := os.MkdirAll(self, 0o755); err != nil {
		t.Fatal(err)
	}
	selfTarget := filepath.Join(dir, "ns-host")
	if err := os.WriteFile(selfTarget, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(selfTarget, filepath.Join(self, "mnt")); err != nil {
		t.Fatal(err)
	}

	d := filepath.Join(dir, itoa(pid))
	if err := os.MkdirAll(filepath.Join(d, "ns"), 0o755); err != nil {
		t.Fatal(err)
	}
	theirTarget := selfTarget
	if !sameNS {
		theirTarget = filepath.Join(dir, "ns-container")
		if err := os.WriteFile(theirTarget, []byte("y"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(theirTarget, filepath.Join(d, "ns", "mnt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(exe, filepath.Join(d, "exe")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "environ"), []byte(env), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/tmp", filepath.Join(d, "cwd")); err != nil {
		t.Fatal(err)
	}
	if refSymlink != "" {
		theirRoot := filepath.Join(d, "root", "root", ".local", "bin")
		if err := os.MkdirAll(theirRoot, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(refSymlink, filepath.Join(theirRoot, "claude")); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const verDir = "/root/.local/share/claude/versions/"

func TestDetectExternalComparesWithContainerInstall(t *testing.T) {
	buildExternal(t, 900, verDir+"2.1.241", "PANEL_RECOVERY=1\x00HOME=/root\x00", verDir+"2.1.246", false)

	got := DetectExternal("PANEL_RECOVERY=1", "recovery")
	if len(got) != 1 {
		t.Fatalf("found %d processes, want 1", len(got))
	}
	p := got[0]
	if p.Version != "2.1.241" || p.Ref != "2.1.246" {
		t.Fatalf("version=%q ref=%q, want 2.1.241 / 2.1.246", p.Version, p.Ref)
	}
	if p.Current {
		t.Fatal("Current=true, but 2.1.241 is BEHIND 2.1.246")
	}
	if p.Target != "recovery" {
		t.Fatalf("Target=%q, want \"recovery\"", p.Target)
	}
}

func TestDetectExternalDoesNotFlagUpToDate(t *testing.T) {
	buildExternal(t, 901, verDir+"2.1.246", "PANEL_RECOVERY=1\x00", verDir+"2.1.246", false)

	got := DetectExternal("PANEL_RECOVERY=1", "recovery")
	if len(got) != 1 {
		t.Fatalf("found %d, want 1", len(got))
	}
	if !got[0].Current {
		t.Fatal("Current=false for a process on the SAME version as its container")
	}
}

func TestDetectExternalIgnoresSameNamespaceProcess(t *testing.T) {
	buildExternal(t, 902, verDir+"2.1.241", "PANEL_RECOVERY=1\x00", verDir+"2.1.246", true)

	if got := DetectExternal("PANEL_RECOVERY=1", "recovery"); len(got) != 0 {
		t.Fatalf("found %d, wanted 0 (same mount namespace)", len(got))
	}
}

func TestDetectExternalRequiresMarker(t *testing.T) {
	buildExternal(t, 903, verDir+"2.1.241", "HOME=/root\x00OTHER=1\x00", verDir+"2.1.246", false)

	if got := DetectExternal("PANEL_RECOVERY=1", "recovery"); len(got) != 0 {
		t.Fatalf("found %d, wanted 0 (no PANEL_RECOVERY)", len(got))
	}
}

func TestDetectExternalNoReferenceReportsNoLag(t *testing.T) {
	buildExternal(t, 904, verDir+"2.1.241", "PANEL_RECOVERY=1\x00", "", false)

	got := DetectExternal("PANEL_RECOVERY=1", "recovery")
	if len(got) != 1 {
		t.Fatalf("found %d, want 1", len(got))
	}
	if got[0].Ref != "" || !got[0].Current {
		t.Fatalf("ref=%q current=%v; with no reference it has to assume up to date", got[0].Ref, got[0].Current)
	}
}

func TestDetectExternalNoMarkerIsEmpty(t *testing.T) {
	buildExternal(t, 905, verDir+"2.1.241", "PANEL_RECOVERY=1\x00", verDir+"2.1.246", false)
	if got := DetectExternal("", "recovery"); len(got) != 0 {
		t.Fatalf("found %d with an empty marker, wanted 0", len(got))
	}
}
