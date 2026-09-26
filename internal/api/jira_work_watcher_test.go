package api

import "testing"

// TestMatchesCompletionUserOnly makes sure the auto-watcher only closes the
// ticket when the USER gives an explicit order — never from the assistant's
// prose (which mentions "done"/"marcar como concluído"/"ficou pronto" all the time).
func TestMatchesCompletionUserOnly(t *testing.T) {
	// Assistant prose (lines starting with "●" / "⎿") must NOT fire, even when
	// it contains every trigger word.
	assistantNoise := "" +
		"● O watcher dispara ao detectar \"pode fechar\", \"marcar como done/concluído\".\n" +
		"● Tá funcionando, ficou perfeito, tudo ok — deploy SUCCESS.\n" +
		"  ⎿ marcar como done\n" +
		"  resolvido e pronto, pode marcar\n" // the assistant's indented continuation
	if matchesCompletion(assistantNoise) {
		t.Errorf("assistant prose should NOT close the ticket")
	}

	// Positive feedback from the user is NOT an order to close.
	for _, s := range []string{
		"❯ ficou perfeito, parabéns",
		"❯ tá funcionando agora",
		"❯ tudo ok",
		"❯ funcionou!",
	} {
		if matchesCompletion(s) {
			t.Errorf("user feedback %q should NOT close", s)
		}
	}

	// An EXPLICIT order from the user MUST close it.
	for _, s := range []string{
		"❯ pode fechar o ticket",
		"❯ pode marcar como done",
		"❯ marcar como concluído",
		"❯ fechar o ticket",
		"❯ /done",
		"❯ pode dar done agora",
	} {
		if !matchesCompletion(s) {
			t.Errorf("explicit user order %q SHOULD close", s)
		}
	}
}
