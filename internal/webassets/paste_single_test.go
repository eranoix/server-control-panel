package webassets

import "testing"

func TestTerminalPasteUploadsOnce(t *testing.T) { runHarness(t, "test-paste-single.mjs") }
