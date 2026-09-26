package pty

import (
	"os"
	"testing"
)

// Dumps to disk EXACTLY the bytes the server delivers to the app in the primer,
// to feed the harness in `scripts/terminal-prova/` without going through
// authentication or through a Python approximation. This is not a test: it is an
// instrument.
//
//	go test ./internal/pty/ -run TestDumpsServedCrop -v \
//	  -args-nao-existe   (use the env: VPSM_DUMP_SESSAO, VPSM_DUMP_SAIDA)
func TestDumpsServedCrop(t *testing.T) {
	session := os.Getenv("VPSM_DUMP_SESSAO")
	output := os.Getenv("VPSM_DUMP_SAIDA")
	if session == "" || output == "" {
		t.Skip("set VPSM_DUMP_SESSAO and VPSM_DUMP_SAIDA")
	}
	data, total := rawLogTail("/opt/panel/data", "sam", session, 4194304)
	t.Logf("session %q: total log %d B, served slice %d B", session, total, len(data))
	if err := os.WriteFile(output, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
