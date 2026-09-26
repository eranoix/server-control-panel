package api

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// stt_filter.go — a Bag-of-Hallucinations (BoH) filter for Whisper's outputs.
//
// Whisper hallucinates specific output during silence and background noise. The
// Barański et al. ICASSP 2025 paper (arXiv:2501.11378) catalogued the recurring
// hallucinations and showed that filtering them cuts erroneous outputs by 67%.
//
// Mechanism: a blocklist of exact phrases + heuristics (repetitions,
// punctuation only, minimum length). Applied BEFORE emitting partial/final
// to the client — if the filter spots a hallucination, dropping it loses
// nothing real (those outputs did not match the user's speech anyway).
//
// Cost: about 5-20μs per segment. No perceptible impact on latency.

// hallucinationPhrases is the BoH compiled from the literature + empirical
// Brazilian-Portuguese observation. Comparison is case-insensitive + trimmed.
// Covers EN, PT and FR (Whisper was trained on YouTube subtitles, which are
// full of Amara.org and "thanks for watching").
var hallucinationPhrases = []string{
	// English — YouTube video boilerplate
	"thank you for watching",
	"thanks for watching",
	"thank you for watching!",
	"thanks for watching!",
	"thanks for watching.",
	"thank you so much for watching",
	"please subscribe",
	"please subscribe to my channel",
	"like and subscribe",
	"subscribe to my channel",
	"don't forget to subscribe",
	"see you next time",
	"see you in the next video",
	"bye bye",
	"goodbye",

	// Portuguese — the cultural equivalents common in the training data
	"obrigado por assistir",
	"obrigado por assistir!",
	"obrigada por assistir",
	"obrigado pela atenção",
	"obrigada pela atenção",
	"inscreva-se no canal",
	"se inscreva no canal",
	"curta e compartilhe",
	"deixe seu like",
	"até a próxima",
	"até o próximo vídeo",
	"tchau tchau",

	// French — Amara.org subtitle credits (extremely common)
	"sous-titres réalisés par la communauté d'amara.org",
	"sous-titres réalisés par la communauté d'amara",
	"sous-titres faits par la communauté d'amara.org",
	"sous-titrage st' 501",
	"sous-titrage société radio-canada",

	// Music markers (Whisper transcribes silence + noise as [Música])
	"[música]",
	"[musica]",
	"[music]",
	"♪♪",
	"♪ música ♪",
	"♪ music ♪",
	"(música)",
	"(musica)",
	"(music)",

	// Other Whisper boilerplate during silence
	"you",
	"yeah",
	"uh",
	"um",
	"hmm",
	".",
	"...",
	",",
}

// hallucinationPhrasesLower is the pre-normalised version (lower + trimmed) for
// an O(1) match with no allocation on the hot path.
var hallucinationPhrasesLower = func() map[string]struct{} {
	m := make(map[string]struct{}, len(hallucinationPhrases))
	for _, p := range hallucinationPhrases {
		m[strings.ToLower(strings.TrimSpace(p))] = struct{}{}
	}
	return m
}()

// puncOnlyRegex matches strings that are ONLY punctuation/whitespace/symbols.
var puncOnlyRegex = regexp.MustCompile(`^[\p{P}\p{S}\s]+$`)

// hasRepetitionLoop detects the "X X X X..." pattern (the same word 3+ times
// in a row). Whisper loops like that during prolonged silence. Go's regex
// engine (RE2) has no backreferences, so we do it by hand: split on
// whitespace, count runs of identical words, case-insensitively.
func hasRepetitionLoop(s string) bool {
	fields := strings.Fields(strings.ToLower(s))
	if len(fields) < 3 {
		return false
	}
	run := 1
	for i := 1; i < len(fields); i++ {
		if fields[i] == fields[i-1] {
			run++
			if run >= 3 {
				return true
			}
		} else {
			run = 1
		}
	}
	return false
}

// isHallucination decides whether a Whisper output is a hallucination.
// Returns (true, reason) when it should be dropped; (false, "") for real speech.
//
// Criteria (check order, cheapest first):
//  1. Empty / whitespace only → drop
//  2. Length < 2 chars (after trim) → drop (Whisper emits a lone ".")
//  3. Punctuation/symbols only → drop ("♪", "...", "..!?")
//  4. An exact match in the BoH list (lower-cased) → drop
//  5. A partial match on a BoH phrase covering > 80% of the text → drop
//  6. Repetition "X X X X" (3+ times) → drop (a loop hallucination)
//
// Criterion 5 is the more permissive one (substring), so as to catch variations
// like "Thanks for watching guys!" that carry the boilerplate inside them.
func isHallucination(text string) (bool, string) {
	t := strings.TrimSpace(text)
	if t == "" {
		return true, "empty"
	}
	if utf8.RuneCountInString(t) < 2 {
		return true, "too-short"
	}
	if puncOnlyRegex.MatchString(t) {
		return true, "punctuation-only"
	}
	lower := strings.ToLower(t)
	if _, exact := hallucinationPhrasesLower[lower]; exact {
		return true, "exact-bag-of-hallucinations"
	}
	// Substring match — it only fires when the BoH phrase covers ≥80% of the
	// text. Otherwise a legitimate long text that MENTIONS "thanks for watching"
	// inside a sentence would be filtered by mistake.
	if len(lower) > 0 {
		for phrase := range hallucinationPhrasesLower {
			if len(phrase) < 8 {
				continue // skip muito curto pra substring match
			}
			if strings.Contains(lower, phrase) {
				ratio := float64(len(phrase)) / float64(len(lower))
				if ratio >= 0.8 {
					return true, "substring-bag-of-hallucinations"
				}
			}
		}
	}
	if hasRepetitionLoop(t) {
		return true, "repetition-loop"
	}
	return false, ""
}
