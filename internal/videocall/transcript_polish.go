package videocall

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"server-control-panel/internal/auth"
)

type aiCallBudget struct {
	mu     sync.Mutex
	hits   map[string][]time.Time
	maxN   int
	window time.Duration
}

func newAICallBudget(maxN int, window time.Duration) *aiCallBudget {
	return &aiCallBudget{
		hits:   map[string][]time.Time{},
		maxN:   maxN,
		window: window,
	}
}

func (b *aiCallBudget) Allow(user string) bool {
	if user == "" {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-b.window)
	bucket := b.hits[user]
	keep := bucket[:0]
	for _, t := range bucket {
		if t.After(cutoff) {
			keep = append(keep, t)
		}
	}
	if len(keep) >= b.maxN {
		b.hits[user] = keep
		return false
	}
	b.hits[user] = append(keep, now)
	return true
}

var (
	polishBudget  = newAICallBudget(12, time.Minute)
	summaryBudget = newAICallBudget(3, time.Minute)
)

func sanitizePromptContext(items []string) []string {
	out := make([]string, 0, len(items))
	for _, c := range items {
		c = strings.ReplaceAll(c, "\n", " ")
		c = strings.ReplaceAll(c, "\r", " ")
		c = strings.ReplaceAll(c, "\x00", "")
		c = strings.ReplaceAll(c, "```", "")
		c = strings.ReplaceAll(c, "<|", "")
		c = strings.ReplaceAll(c, "|>", "")
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if utf8.RuneCountInString(c) > 200 {
			runes := []rune(c)
			c = string(runes[:200])
		}
		out = append(out, c)
		if len(out) >= 5 {
			break
		}
	}
	return out
}

func (s *Service) HandleTranscriptPolish(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user := auth.UserFrom(r)
	if user == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !polishBudget.Allow(user) {
		http.Error(w, "rate limit — wait a few seconds", http.StatusTooManyRequests)
		return
	}
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		http.Error(w, "ANTHROPIC_API_KEY not configured — add it to the systemd drop-in", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		Text    string   `json:"text"`
		Lang    string   `json:"lang"`
		Context []string `json:"context"`
		Speaker string   `json:"speaker"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	req.Text = strings.TrimSpace(req.Text)
	if req.Text == "" {
		http.Error(w, "empty text", http.StatusBadRequest)
		return
	}
	if utf8.RuneCountInString(req.Text) > 2000 {
		http.Error(w, "text too long (>2000 chars)", http.StatusBadRequest)
		return
	}
	if req.Lang == "" {
		req.Lang = "en-US"
	}
	if len(req.Context) > 5 {
		req.Context = req.Context[len(req.Context)-5:]
	}
	req.Context = sanitizePromptContext(req.Context)
	if utf8.RuneCountInString(req.Speaker) > 60 {
		runes := []rune(req.Speaker)
		req.Speaker = string(runes[:60])
	}
	req.Speaker = strings.ReplaceAll(req.Speaker, "\n", " ")
	req.Speaker = strings.ReplaceAll(req.Speaker, "\r", " ")

	prompt := buildPolishPrompt(req.Text, req.Lang, req.Context, req.Speaker)
	polished, err := callAnthropic(r.Context(), apiKey, prompt)
	if err != nil {
		http.Error(w, "anthropic: "+err.Error(), http.StatusBadGateway)
		return
	}
	polished = extractPolished(polished, req.Text)
	writeJSONHTTP(w, map[string]string{"polished": polished})
}

func buildPolishPrompt(text, lang string, context []string, speaker string) string {
	langName := languageHumanName(lang)
	var sb strings.Builder
	sb.WriteString("You correct automatic speech transcriptions in " + langName + ". Your task is to rewrite ONE utterance the Web Speech API captured roughly, making it readable WITHOUT changing its meaning.\n\n")
	sb.WriteString("HARD RULES:\n")
	sb.WriteString("1. Add correct punctuation (comma, full stop, question mark, exclamation mark).\n")
	sb.WriteString("2. Remove natural hesitations: \"uh\", \"um\", \"like\", \"you know\", repeated fillers, duplicated words.\n")
	sb.WriteString("3. Capitalize the start of sentences and proper nouns.\n")
	sb.WriteString("4. If a word is obviously wrong because of speech recognition (a homophone in context), fix it. E.g. \"their\" → \"there\" when the context calls for it.\n")
	sb.WriteString("5. NEVER add information that is not in the utterance. NEVER make anything up.\n")
	sb.WriteString("6. NEVER change the tone (keep it formal or informal as it is).\n")
	sb.WriteString("7. NEVER translate. Keep the original language.\n")
	sb.WriteString("8. If the utterance is very short (1-3 words) or already perfect, return it as is or with punctuation only.\n")
	sb.WriteString("9. REPLY WITH the polished version ONLY. No explanation, no quotes, no prefix.\n\n")

	if len(context) > 0 {
		sb.WriteString("CONTEXT (earlier utterances in the SAME conversation, so you understand the topic):\n")
		for _, c := range context {
			sb.WriteString("- " + c + "\n")
		}
		sb.WriteString("\n")
	}
	if speaker != "" {
		sb.WriteString("Who is speaking now: " + speaker + "\n\n")
	}
	sb.WriteString(fmt.Sprintf("RAW UTTERANCE TO POLISH:\n%s\n\nPOLISHED VERSION:", text))
	return sb.String()
}

func extractPolished(resp, original string) string {
	r := strings.TrimSpace(resp)
	for _, prefix := range []string{"Polished version:", "POLISHED VERSION:", "Response:"} {
		r = strings.TrimPrefix(r, prefix)
		r = strings.TrimPrefix(r, strings.ToLower(prefix))
	}
	r = strings.TrimSpace(r)
	if len(r) >= 2 {
		first, last := r[0], r[len(r)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			r = r[1 : len(r)-1]
		}
	}
	r = strings.TrimSpace(r)
	if r == "" {
		return original
	}
	if utf8.RuneCountInString(r) > 5*utf8.RuneCountInString(original)+200 {
		return original
	}
	return r
}

func languageHumanName(bcp string) string {
	switch bcp {
	case "pt-BR":
		return "Brazilian Portuguese"
	case "en-US":
		return "American English"
	case "es-ES":
		return "Spanish"
	case "fr-FR":
		return "French"
	case "it-IT":
		return "Italian"
	case "de-DE":
		return "German"
	case "ja-JP":
		return "Japanese"
	default:
		return bcp
	}
}
