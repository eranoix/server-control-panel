package pty

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDtachChatterDoesNotLeakAtBlockBoundary(t *testing.T) {
	cases := []struct {
		name   string
		blocks []string
	}{
		{"farewell in a single chunk", []string{"\x1b[999H\r\n[detached]\r\n\x1b[?25h"}},
		{"farewell split in the middle of the literal", []string{"\x1b[999H\r\n[deta", "ched]\r\n\x1b[?25h"}},
		{"farewell split before the literal", []string{"\x1b[999H\r\n", "[detached]\r\n\x1b[?25h"}},
		{"farewell byte by byte", func() []string {
			var b []string
			for _, r := range "\x1b[999H\r\n[detached]\r\n\x1b[?25h" {
				b = append(b, string(r))
			}
			return b
		}()},
		{"attach clear in a single chunk", []string{"\x1b[H\x1b[Jreal content\r\n"}},
		{"attach clear split", []string{"\x1b[H", "\x1b[Jreal content\r\n"}},
		{"attach clear byte by byte", []string{"\x1b", "[", "H", "\x1b", "[", "J", "real content\r\n"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			w, _, _, release := acquireSessionLog(dir, "u", "s")
			for _, b := range tc.blocks {
				if _, err := w.Write([]byte(b)); err != nil {
					t.Fatalf("write: %v", err)
				}
			}
			release()
			d, _ := os.ReadFile(sessionLogPath(dir, "u", "s"))
			got := string(d)
			for _, junk := range []struct{ name, seq string }{
				{"[detached]", "[detached]"},
				{"ESC[999H (scrolls the whole screen)", "\x1b[999H"},
				{"ESC[H ESC[J (clears the screen)", "\x1b[H\x1b[J"},
			} {
				if strings.Contains(got, junk.seq) {
					t.Errorf("leaked %s into the log", junk.name)
				}
			}
		})
	}
}

func TestRealContentSurvivesFilter(t *testing.T) {
	dir := t.TempDir()
	w, _, _, release := acquireSessionLog(dir, "u", "s")
	_, _ = w.Write([]byte("\x1b[H"))
	_, _ = w.Write([]byte("\x1b[Jhello"))
	_, _ = w.Write([]byte(" world\r\n"))
	release()
	d, _ := os.ReadFile(sessionLogPath(dir, "u", "s"))
	if got, want := string(d), "hello world\r\n"; got != want {
		t.Errorf("log = %q; want %q", got, want)
	}
}

func TestWordDetachedInProgramOutputDoesNotSilenceLog(t *testing.T) {
	dir := t.TempDir()
	w, _, _, release := acquireSessionLog(dir, "u", "s")
	_, _ = w.Write([]byte("the dtach binary writes [detached] on exit\r\n"))
	_, _ = w.Write([]byte("next line, which must exist\r\n"))
	release()
	d, _ := os.ReadFile(sessionLogPath(dir, "u", "s"))
	if !strings.Contains(string(d), "next line") {
		t.Error("the connection stopped recording because of a word in the program's output")
	}
}

func TestMouseFilterDoesNotEatDeleteLine(t *testing.T) {
	entry := []byte("before\x1b[Mafter all of this\r\n")
	output := stripMouseReports(entry)
	if !bytes.Contains(output, []byte("after all of this")) {
		t.Errorf("the filter ate content after CSI M: %q", output)
	}
	if !bytes.Equal(entry, output) {
		t.Errorf("CSI M is not a mouse report; the stream had to pass through intact.\n had: %q\n became: %q", entry, output)
	}
}

func TestMouseFilterStillEatsSGRReport(t *testing.T) {
	output := stripMouseReports([]byte("before\x1b[<35;80;24Mafter\r\n"))
	if bytes.Contains(output, []byte("35;80;24")) {
		t.Errorf("the SGR report got through: %q", output)
	}
	if !bytes.Contains(output, []byte("beforeafter")) {
		t.Errorf("the filter took content with it: %q", output)
	}
}

func TestAttachReplayDoesNotEndInDtachMessage(t *testing.T) {
	dir := t.TempDir()
	path := sessionLogPath(dir, "u", "s")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	content := "real line\r\n" + strings.Repeat("more text\r\n", 20) + "\x1b[H\x1b[J"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	output := attachReplay(dir, "u", "s")
	if bytes.HasSuffix(output, dtachAttachClear) {
		t.Error("the replay ends in \"clear the screen\" — the client paints everything and then clears it")
	}
	if !bytes.Contains(output, []byte("real line")) {
		t.Error("the actual content disappeared from the replay")
	}
}
