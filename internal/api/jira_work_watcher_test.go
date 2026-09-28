package api

import "testing"

func TestMatchesCompletionUserOnly(t *testing.T) {
	assistantNoise := "" +
		"● The watcher fires on \"can close it\", \"mark as done/resolved\".\n" +
		"● It works, looks perfect, all ok: deploy SUCCESS.\n" +
		"  ⎿ mark as done\n" +
		"  resolved and ready, you can mark it\n"
	if matchesCompletion(assistantNoise) {
		t.Errorf("assistant prose should NOT close the ticket")
	}

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
