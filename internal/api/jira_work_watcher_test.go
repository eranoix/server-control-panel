package api

import "testing"

// TestMatchesCompletionUserOnly makes sure the auto-watcher only closes the
// ticket when the USER gives an explicit order — never from the assistant's
// prose (which mentions "done"/"mark as done" all the time).
func TestMatchesCompletionUserOnly(t *testing.T) {
	// Assistant prose (lines starting with "●" / "⎿") must NOT fire, even when
	// it contains every trigger word.
	assistantNoise := "" +
		"● The watcher fires on \"can close it\", \"mark as done/resolved\".\n" +
		"● It works, looks perfect, all ok: deploy SUCCESS.\n" +
		"  ⎿ mark as done\n" +
		"  resolved and ready, you can mark it\n" // the assistant's indented continuation
	if matchesCompletion(assistantNoise) {
		t.Errorf("assistant prose should NOT close the ticket")
	}

	// Positive feedback from the user is NOT an order to close.
	for _, s := range []string{
		"❯ looks perfect, congrats",
		"❯ it works now",
		"❯ all ok",
		"❯ it worked!",
	} {
		if matchesCompletion(s) {
			t.Errorf("user feedback %q should NOT close", s)
		}
	}

	// An EXPLICIT order from the user MUST close it.
	for _, s := range []string{
		"❯ you can close it",
		"❯ you can mark it as done",
		"❯ mark as resolved",
		"❯ close the ticket",
		"❯ /done",
		"❯ mark it done now",
	} {
		if !matchesCompletion(s) {
			t.Errorf("explicit user order %q SHOULD close", s)
		}
	}
}
