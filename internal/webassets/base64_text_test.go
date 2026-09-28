package webassets

import "testing"

func TestTerminalBase64ArrivesAsUTF8(t *testing.T) { runHarness(t, "test-base64-text.mjs") }
