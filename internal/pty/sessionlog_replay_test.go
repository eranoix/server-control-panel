package pty

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSessionInAltScreen(t *testing.T) {
	cases := []struct {
		name string
		data string
		want bool
	}{
		{"empty", "", false},
		{"plain shell", "$ ls\r\nfoo bar\r\n$ ", false},
		{"entered and stayed (live TUI)", "prompt\r\n\x1b[?1049hclaude drawing", true},
		{"entered and left (back to the shell)", "\x1b[?1049hTUI\x1b[?1049l\r\n$ ", false},
		{"several switches, last is enter", "\x1b[?1049hA\x1b[?1049l\x1b[?1049hB", true},
		{"several switches, last is leave", "\x1b[?1049hA\x1b[?1049lB\x1b[?1049hC\x1b[?1049l$ ", false},
		{"?47h variant", "x\x1b[?47hvim", true},
	}
	for _, c := range cases {
		if got := sessionInAltScreen([]byte(c.data)); got != c.want {
			t.Errorf("%s: sessionInAltScreen = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestAttachReplay(t *testing.T) {
	dir := t.TempDir()
	user, name := "u", "s"
	logp := sessionLogPath(dir, user, name)
	if err := os.MkdirAll(filepath.Dir(logp), 0o700); err != nil {
		t.Fatal(err)
	}

	// (1) a normal shell → the replay returns the content.
	if err := os.WriteFile(logp, []byte("$ echo hi\r\nhi\r\n$ "), 0o600); err != nil {
		t.Fatal(err)
	}
	if rep := attachReplay(dir, user, name); len(rep) == 0 || !strings.Contains(string(rep), "hi") {
		t.Errorf("normal shell: expected a replay with the history, got %q", string(rep))
	}

	// (2) a TUI session (alt-screen open) → replay skipped (nil).
	if err := os.WriteFile(logp, []byte("$ claude\r\n\x1b[?1049hclaude frame"), 0o600); err != nil {
		t.Fatal(err)
	}
	if rep := attachReplay(dir, user, name); rep != nil {
		t.Errorf("alt-screen: expected nil (skipped), got %q", string(rep))
	}

	// (3) missing log → nil, no panic.
	if rep := attachReplay(dir, "naoexiste", "naoexiste"); rep != nil {
		t.Errorf("log missing: expected nil, got %q", string(rep))
	}

	// (3b) rotation: the alt-screen ENTER stayed in .1, the tail in .log → it must
	// detect alt-screen (scanning .1+.log) and SKIP the replay (otherwise it throws
	// garbage into a TUI).
	if err := os.WriteFile(logp+".1", []byte("$ claude\r\n\x1b[?1049hold claude frame"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logp, []byte("\x1b[2Jmore TUI drawing with no enter here"), 0o600); err != nil {
		t.Fatal(err)
	}
	if rep := attachReplay(dir, user, name); rep != nil {
		t.Errorf("alt-screen post-rotation: expected nil (detect enter in .1), got %q", string(rep))
	}
	_ = os.Remove(logp + ".1")

	// (4) byte ceiling: it starts on a line boundary and respects the cap.
	big := strings.Repeat("scrollback line goes here\r\n", 20000) // ~ 500 KiB
	if err := os.WriteFile(logp, []byte(big), 0o600); err != nil {
		t.Fatal(err)
	}
	rep := attachReplay(dir, user, name)
	if len(rep) == 0 || len(rep) > maxAttachReplayBytes {
		t.Errorf("cap: len=%d, expected >0 and <= %d", len(rep), maxAttachReplayBytes)
	}
	if len(rep) > 0 && rep[0] == '\n' {
		t.Errorf("cap: replay should not start with an orphan \\n")
	}
}

// TestStripMouseReports pins the filter that strips out of the replay the
// mouse reports recorded in the log. Table-driven because the danger here is at
// the edges: the 128 KiB cut can land in the middle of a sequence, and a scanner
// that trusts it will find the terminator reads past the end of the buffer.
func TestStripMouseReports(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"plain text passes intact", "hello world\n", "hello world\n"},
		{"SGR press and release vanish", "a\x1b[<35;80;24Mb\x1b[<35;80;24mc", "abc"},
		// `ESC [ M` is indistinguishable from `CSI M`, which in ECMA-48 is DL
		// (Delete Line) — an everyday editor sequence. The old branch
		// ate the sequence AND THE THREE FOLLOWING BYTES, which in a DL are
		// content. Both sides are unlikely, but not equally so: the
		// X10 only shows up if some program asks for tracking mode 9,
		// which practically nothing has asked for in decades; DL comes out of any editor.
		// Measured across the 28 logs on this machine: ZERO occurrences of `ESC[M`.
		{"CSI M (Delete Line) passes intact with its content", "a\x1b[M 0@b", "a\x1b[M 0@b"},
		{"CSI M at the end also passes", "a\x1b[M ", "a\x1b[M "},
		{"SGR truncated at the end vanishes entirely", "a\x1b[<35;80;", "a"},
		{"CSI that is NOT mouse stays", "a\x1b[31mred\x1b[0m", "a\x1b[31mred\x1b[0m"},
		{"CSI with a letter among the digits stays", "a\x1b[<35;8x0M", "a\x1b[<35;8x0M"},
		{"several in a row all vanish", "\x1b[<0;1;1M\x1b[<0;2;2M\x1b[<0;3;3mend", "end"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := string(stripMouseReports([]byte(c.input)))
			if got != c.want {
				t.Errorf("stripMouseReports(%q) = %q, want %q", c.input, got, c.want)
			}
		})
	}
}

// TestStripMouseReports_NoAllocWhenNotNeeded: the common case is a log
// with no mouse byte at all, and it must not pay for a 128 KiB copy on every
// attach.
func TestStripMouseReports_NoAllocWhenNotNeeded(t *testing.T) {
	entry := []byte("line 1\nline 2\n\x1b[32mgreen\x1b[0m\n")
	output := stripMouseReports(entry)
	if &entry[0] != &output[0] {
		t.Error("with no mouse report, the buffer has to come back as it arrived (same memory)")
	}
}
