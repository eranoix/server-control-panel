package webassets

import "testing"

// Copying from a terminal app broke accented text: OSC 52 sends the copied
// text as base64 and the handler decoded it with atob(), which returns one
// character per byte. The same atob read the JWT payload and rejected
// base64url, which silently disabled the early login refresh.
//
// The harness runs the real base64ToText against what a terminal sends and
// fails if atob reads text anywhere else. It is plain Node (no browser).
func TestTerminalBase64ArrivesAsUTF8(t *testing.T) { runHarness(t, "test-base64-text.mjs") }
