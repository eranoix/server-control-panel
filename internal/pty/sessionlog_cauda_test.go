package pty

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// lerCauda reaches across the rotation without loading the whole log. What it
// returns has to be byte for byte what the naive read would return — otherwise
// the panel's primer rebuilds a screen different from the one the terminal would give.
func TestLerCaudaBateComALeituraInteira(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.log")
	anterior := []byte(strings.Repeat("A", 5000))
	atual := []byte(strings.Repeat("B", 3000))
	if err := os.WriteFile(path+".1", anterior, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, atual, 0o600); err != nil {
		t.Fatal(err)
	}
	inteiro := append(append([]byte{}, anterior...), atual...)

	for _, quanto := range []int{1, 100, 2999, 3000, 3001, 7999, 8000, 9000} {
		got, total := lerCauda(path, quanto)
		if total != len(inteiro) {
			t.Fatalf("quanto=%d: total=%d; want %d", quanto, total, len(inteiro))
		}
		esperado := inteiro
		if quanto < len(inteiro) {
			esperado = inteiro[len(inteiro)-quanto:]
		}
		if !bytes.Equal(got, esperado) {
			t.Errorf("quanto=%d: returned %d bytes (%q…%q); wanted %d",
				quanto, len(got), primeiros(got), ultimos(got), len(esperado))
		}
	}
}

// With no previous generation, and with an empty log: the two edge cases the
// server meets on a freshly created session.
func TestLerCaudaNasBordas(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.log")

	if d, total := lerCauda(path, 100); d != nil || total != 0 {
		t.Errorf("nonexistent log returned %d bytes/total %d; wanted nothing", len(d), total)
	}
	if err := os.WriteFile(path, []byte("só o atual"), 0o600); err != nil {
		t.Fatal(err)
	}
	d, total := lerCauda(path, 100)
	if string(d) != "só o atual" || total != len("só o atual") {
		t.Errorf("with no earlier generation: %q/%d", d, total)
	}
}

func primeiros(b []byte) string {
	if len(b) > 8 {
		return string(b[:8])
	}
	return string(b)
}

func ultimos(b []byte) string {
	if len(b) > 8 {
		return string(b[len(b)-8:])
	}
	return string(b)
}
