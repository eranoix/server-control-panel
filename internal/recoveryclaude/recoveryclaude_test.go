package recoveryclaude

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The path the "Start the container" button walks: materialize the embedded
// assets and run the manager from there. Two earlier versions died with "no
// such file or directory" because they read the repository from disk — this
// test exercises the real path instead of asserting about the text of the code.
func TestMaterializaEntregaUmGerenciadorExecutavel(t *testing.T) {
	dir := t.TempDir()
	script, err := Materializa(dir)
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}

	info, err := os.Stat(script)
	if err != nil {
		t.Fatalf("the manager was not written: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("the manager has no execute bit (%v) — the exec would fail", info.Mode().Perm())
	}

	// Docker's build context is the materialized directory, so the Dockerfile
	// and the scripts it copies have to come out TOGETHER. With one missing, the
	// `build` would break only when it mattered.
	for _, nome := range []string{"Dockerfile", "entrypoint.sh", "bemvindo.sh", "manage.sh"} {
		if _, err := os.Stat(filepath.Join(dir, "recovery-claude", nome)); err != nil {
			t.Errorf("%s was not materialized: %v", nome, err)
		}
	}
}

// The container's independence is an ABSENCE (there is no ANTHROPIC_BASE_URL),
// and absences disappear without anyone noticing. Here it is asserted over the
// content the BINARY carries — not over the repository file, which may diverge
// from what was actually embedded.
func TestOQueOBinarioCarregaNaoApontaProRouter(t *testing.T) {
	dir := t.TempDir()
	if _, err := Materializa(dir); err != nil {
		t.Fatalf("materialize: %v", err)
	}
	// Only DEFINING/INJECTING the variable breaks the independence. Mentioning
	// it does not — the manager's own `doctor` has to cite it in order to VERIFY
	// that it is absent, and the first version of this test failed exactly the
	// check that protects the guarantee.
	injeta := []string{
		`ENV ANTHROPIC_BASE_URL`,    // Dockerfile
		`export ANTHROPIC_BASE_URL`, // shell
		`-e ANTHROPIC_BASE_URL`,     // docker run
		`"ANTHROPIC_BASE_URL"`,      // generated settings.json
		`ANTHROPIC_BASE_URL=http`,   // direct assignment
	}
	for _, nome := range []string{"Dockerfile", "entrypoint.sh", "manage.sh"} {
		b, err := os.ReadFile(filepath.Join(dir, "recovery-claude", nome))
		if err != nil {
			t.Fatalf("reading %s: %v", nome, err)
		}
		for _, linha := range strings.Split(string(b), "\n") {
			corte := strings.TrimSpace(linha)
			if strings.HasPrefix(corte, "#") {
				continue // a comment explaining the absence is welcome
			}
			for _, padrao := range injeta {
				if strings.Contains(corte, padrao) {
					t.Errorf("embedded %s points Claude at the router (%s): %q", nome, padrao, corte)
				}
			}
		}
	}
}

// Always rewriting (instead of skipping when it already exists) is what stops
// an old deploy's version from surviving on disk after a fix.
func TestMaterializaSobrescreveVersaoAntiga(t *testing.T) {
	dir := t.TempDir()
	if _, err := Materializa(dir); err != nil {
		t.Fatalf("materialize: %v", err)
	}
	alvo := filepath.Join(dir, "recovery-claude", "manage.sh")
	if err := os.WriteFile(alvo, []byte("#!/bin/sh\necho versao velha\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Materializa(dir); err != nil {
		t.Fatalf("materialize again: %v", err)
	}
	b, _ := os.ReadFile(alvo)
	if strings.Contains(string(b), "versao velha") {
		t.Error("the old manager survived — a corrected deploy would not reach the disk")
	}
}

// A real execution: the materialized manager has to RUN and talk to Docker.
// Without Docker (CI) it skips — faking coverage would be worse than none.
func TestGerenciadorMaterializadoRodaDeVerdade(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker missing in this environment")
	}
	cmd, err := Comando(t.TempDir(), "status")
	if err != nil {
		t.Fatalf("command: %v", err)
	}
	saida, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the materialized manager did not run: %v\n%s", err, saida)
	}
	if !strings.Contains(string(saida), "estado:") {
		t.Errorf("unexpected output from the manager:\n%s", saida)
	}
}
