package claudever

import (
	"os"
	"path/filepath"
	"testing"
)

// montaExterno writes a fake /proc with a process in ANOTHER mount namespace
// (which is what marks a container), with its environ and its root at <pid>/root.
func montaExterno(t *testing.T, pid int, exe, env, refSymlink string, mesmoNS bool) string {
	t.Helper()
	dir := t.TempDir()
	anterior := raizProc
	raizProc = dir
	t.Cleanup(func() { raizProc = anterior })

	// /proc/self/ns/mnt — the "server's" namespace
	self := filepath.Join(dir, "self", "ns")
	if err := os.MkdirAll(self, 0o755); err != nil {
		t.Fatal(err)
	}
	alvoSelf := filepath.Join(dir, "ns-host")
	if err := os.WriteFile(alvoSelf, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(alvoSelf, filepath.Join(self, "mnt")); err != nil {
		t.Fatal(err)
	}

	d := filepath.Join(dir, itoa(pid))
	if err := os.MkdirAll(filepath.Join(d, "ns"), 0o755); err != nil {
		t.Fatal(err)
	}
	// same ns → points at the same file; different → another file
	alvoDele := alvoSelf
	if !mesmoNS {
		alvoDele = filepath.Join(dir, "ns-container")
		if err := os.WriteFile(alvoDele, []byte("y"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(alvoDele, filepath.Join(d, "ns", "mnt")); err != nil {
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
		raizDele := filepath.Join(d, "root", "root", ".local", "bin")
		if err := os.MkdirAll(raizDele, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(refSymlink, filepath.Join(raizDele, "claude")); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const verDir = "/root/.local/share/claude/versions/"

// The point of the feature: the container's reference is ITS OWN installation,
// not the host's. A process on 2.1.241 with the container on 2.1.246 = really behind.
func TestLevantarExternoComparaComAInstalacaoDoContainer(t *testing.T) {
	montaExterno(t, 900, verDir+"2.1.241", "VPSM_RECOVERY=1\x00HOME=/root\x00", verDir+"2.1.246", false)

	got := LevantarExterno("VPSM_RECOVERY=1", "recovery")
	if len(got) != 1 {
		t.Fatalf("found %d processes, want 1", len(got))
	}
	p := got[0]
	if p.Versao != "2.1.241" || p.Ref != "2.1.246" {
		t.Fatalf("versao=%q ref=%q, want 2.1.241 / 2.1.246", p.Versao, p.Ref)
	}
	if p.Atual {
		t.Fatal("Atual=true, but 2.1.241 is BEHIND 2.1.246")
	}
	if p.Alvo != "recovery" {
		t.Fatalf("Alvo=%q, want \"recovery\"", p.Alvo)
	}
}

// The regression mesmoMount had already fixed and that this code must NOT
// reintroduce: a container NEWER than the host is not behind. Here the host does
// not even enter the tally — the reference is the container's own.
func TestLevantarExternoNaoChamaDeDefasadoQuemEstaEmDia(t *testing.T) {
	montaExterno(t, 901, verDir+"2.1.246", "VPSM_RECOVERY=1\x00", verDir+"2.1.246", false)

	got := LevantarExterno("VPSM_RECOVERY=1", "recovery")
	if len(got) != 1 {
		t.Fatalf("found %d, want 1", len(got))
	}
	if !got[0].Atual {
		t.Fatal("Atual=false for a process on the SAME version as its container")
	}
}

// A HOST process must not leak in here — Levantar takes care of it, with the
// host's reference. Listing it twice would give two rows for the same Claude.
func TestLevantarExternoIgnoraProcessoDoMesmoNamespace(t *testing.T) {
	montaExterno(t, 902, verDir+"2.1.241", "VPSM_RECOVERY=1\x00", verDir+"2.1.246", true)

	if got := LevantarExterno("VPSM_RECOVERY=1", "recovery"); len(got) != 0 {
		t.Fatalf("found %d, wanted 0 (same mount namespace)", len(got))
	}
}

// A container WITHOUT the marker is not the recovery one — restarting the wrong
// container would take something else down.
func TestLevantarExternoExigeOMarcador(t *testing.T) {
	montaExterno(t, 903, verDir+"2.1.241", "HOME=/root\x00OUTRO=1\x00", verDir+"2.1.246", false)

	if got := LevantarExterno("VPSM_RECOVERY=1", "recovery"); len(got) != 0 {
		t.Fatalf("found %d, wanted 0 (no VPSM_RECOVERY)", len(got))
	}
}

// With no readable reference symlink there is no way to assert being behind — and
// asserting "behind" with no basis would make the panel ask for a pointless restart.
func TestLevantarExternoSemReferenciaNaoAcusaDefasagem(t *testing.T) {
	montaExterno(t, 904, verDir+"2.1.241", "VPSM_RECOVERY=1\x00", "", false)

	got := LevantarExterno("VPSM_RECOVERY=1", "recovery")
	if len(got) != 1 {
		t.Fatalf("found %d, want 1", len(got))
	}
	if got[0].Ref != "" || !got[0].Atual {
		t.Fatalf("ref=%q atual=%v; with no reference it has to assume up to date", got[0].Ref, got[0].Atual)
	}
}

func TestLevantarExternoSemMarcadorVazio(t *testing.T) {
	montaExterno(t, 905, verDir+"2.1.241", "VPSM_RECOVERY=1\x00", verDir+"2.1.246", false)
	if got := LevantarExterno("", "recovery"); len(got) != 0 {
		t.Fatalf("found %d with an empty marker, wanted 0", len(got))
	}
}
