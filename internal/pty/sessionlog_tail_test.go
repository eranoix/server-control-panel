package pty

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadTailMatchesFullRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.log")
	anterior := []byte(strings.Repeat("A", 5000))
	current := []byte(strings.Repeat("B", 3000))
	if err := os.WriteFile(path+".1", anterior, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, current, 0o600); err != nil {
		t.Fatal(err)
	}
	whole := append(append([]byte{}, anterior...), current...)

	for _, amount := range []int{1, 100, 2999, 3000, 3001, 7999, 8000, 9000} {
		got, total := readTail(path, amount)
		if total != len(whole) {
			t.Fatalf("amount=%d: total=%d; want %d", amount, total, len(whole))
		}
		expected := whole
		if amount < len(whole) {
			expected = whole[len(whole)-amount:]
		}
		if !bytes.Equal(got, expected) {
			t.Errorf("amount=%d: returned %d bytes (%q…%q); wanted %d",
				amount, len(got), firstBytes(got), lastBytes(got), len(expected))
		}
	}
}

func TestReadTailAtEdges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.log")

	if d, total := readTail(path, 100); d != nil || total != 0 {
		t.Errorf("nonexistent log returned %d bytes/total %d; wanted nothing", len(d), total)
	}
	if err := os.WriteFile(path, []byte("only the current one"), 0o600); err != nil {
		t.Fatal(err)
	}
	d, total := readTail(path, 100)
	if string(d) != "only the current one" || total != len("only the current one") {
		t.Errorf("with no earlier generation: %q/%d", d, total)
	}
}

func firstBytes(b []byte) string {
	if len(b) > 8 {
		return string(b[:8])
	}
	return string(b)
}

func lastBytes(b []byte) string {
	if len(b) > 8 {
		return string(b[len(b)-8:])
	}
	return string(b)
}
