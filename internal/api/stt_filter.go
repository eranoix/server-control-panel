package api

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

var hallucinationPhrases = []string{
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

	"thank you for your attention",
	"thanks for your attention",
	"subscribe to the channel",
	"like and share",
	"leave a like",
	"see you soon",
	"see you in the next one",

	"subtitles by the amara.org community",
	"subtitles by the amara community",
	"subtitles made by the amara.org community",

	"[music]",
	"♪♪",
	"♪ music ♪",
	"(music)",
	"[silence]",
	"[blank_audio]",

	"you",
	"yeah",
	"uh",
	"um",
	"hmm",
	".",
	"...",
	",",
}

var hallucinationPhrasesLower = func() map[string]struct{} {
	m := make(map[string]struct{}, len(hallucinationPhrases))
	for _, p := range hallucinationPhrases {
		m[strings.ToLower(strings.TrimSpace(p))] = struct{}{}
	}
	return m
}()

var puncOnlyRegex = regexp.MustCompile(`^[\p{P}\p{S}\s]+$`)

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
	if len(lower) > 0 {
		for phrase := range hallucinationPhrasesLower {
			if len(phrase) < 8 {
				continue
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
