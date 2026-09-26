package pty

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// escreveLog builds both generations of a session's log and returns the dataDir.
func escreveLog(t *testing.T, geracaoAnterior, atual string) string {
	t.Helper()
	dd := t.TempDir()
	caminho := sessionLogPath(dd, "sam", "Aplicativo")
	if err := os.MkdirAll(filepath.Dir(caminho), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if geracaoAnterior != "" {
		if err := os.WriteFile(caminho+".1", []byte(geracaoAnterior), 0o600); err != nil {
			t.Fatalf("escreve .1: %v", err)
		}
	}
	if err := os.WriteFile(caminho, []byte(atual), 0o600); err != nil {
		t.Fatalf("escreve log: %v", err)
	}
	return dd
}

// A log that fits entirely in the request comes out whole, and Total is the real
// size — Total is how the app can say "this is all there is".
func TestRawLogTail_LogInteiroQuandoCabe(t *testing.T) {
	dd := escreveLog(t, "velho\n", "novo\n")

	data, total := rawLogTail(dd, "sam", "Aplicativo", 1<<20)

	if got := string(data); got != "velho\nnovo\n" {
		t.Fatalf("data = %q, wanted the two generations concatenated", got)
	}
	if total != len(data) {
		t.Fatalf("total = %d, want %d (the whole log fit)", total, len(data))
	}
}

// Cutting by byte can land in the middle of an escape sequence, and half a
// sequence is garbage PRINTED on the operator's screen (the rest of it becomes
// text). The cut advances past the first line break, and this test proves the
// split sequence does not survive.
func TestRawLogTail_CortaEmQuebraDeLinhaNuncaNoMeioDeUmEscape(t *testing.T) {
	completo := strings.Repeat("preenchimento\n", 100) + "\x1b[31mvermelho\x1b[0m\nfim\n"
	dd := escreveLog(t, "", completo)

	// A ceiling that lands INSIDE the "\x1b[31m" if nobody fixes the start.
	alvo := len("vermelho\x1b[0m\nfim\n") + 4

	data, total := rawLogTail(dd, "sam", "Aplicativo", alvo)

	if total != len(completo) {
		t.Fatalf("total = %d, wanted %d (the total is the log's, not the slice's)", total, len(completo))
	}
	if len(data) >= len(completo) {
		t.Fatalf("the slice should be smaller than the log (%d >= %d)", len(data), len(completo))
	}
	if strings.HasPrefix(string(data), "31m") || strings.HasPrefix(string(data), "[31m") {
		t.Fatalf("slice started in the middle of an escape: %q", string(data))
	}
	if strings.Contains(string(data), "\n") {
		// If any break survived, whatever follows it has to be intact.
		if !strings.HasSuffix(string(data), "fim\n") {
			t.Fatalf("slice did not end at the end of the log: %q", string(data))
		}
	}
}

// A session with no log at all (it never had a client attached) is a normal case, not an error.
func TestRawLogTail_SemLogDevolveVazio(t *testing.T) {
	data, total := rawLogTail(t.TempDir(), "sam", "nunca-existiu", 1<<20)
	if data != nil || total != 0 {
		t.Fatalf("want (nil, 0), got (%q, %d)", string(data), total)
	}
}
