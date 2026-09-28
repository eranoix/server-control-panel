package videocall

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"server-control-panel/internal/auth"
)

const (
	anthropicMessagesURL = "https://api.anthropic.com/v1/messages"
	anthropicModel       = "claude-haiku-4-5-20251001"
	anthropicVersion     = "2023-06-01"
	summarizeMaxTokens   = 1024
	transcriptMaxRunes   = 80_000
)

func (s *Service) HandleSummarize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user := auth.UserFrom(r)
	if user == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if s.Recordings == nil {
		http.Error(w, "recordings disabled", http.StatusServiceUnavailable)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/videocall/recordings/")
	parts := strings.SplitN(rest, "/", 2)
	id := parts[0]
	rec, ok := s.Recordings.Get(user, id)
	if !ok {
		http.Error(w, "recording not found", http.StatusNotFound)
		return
	}
	if rec.HasSummary {
		sum, err := s.Recordings.Summary(user, id)
		if err == nil {
			writeJSONHTTP(w, map[string]string{"summary": sum, "cached": "1"})
			return
		}
	}
	if !summaryBudget.Allow(user) {
		http.Error(w, "rate limit on summary — wait 30s", http.StatusTooManyRequests)
		return
	}
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		http.Error(w, "ANTHROPIC_API_KEY not configured — add it to the systemd drop-in and restart", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		Transcript string `json:"transcript"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	transcript := strings.TrimSpace(req.Transcript)
	if transcript == "" {
		http.Error(w, "empty transcript — turn on captions during the call to get a summary", http.StatusBadRequest)
		return
	}
	if utf8.RuneCountInString(transcript) > transcriptMaxRunes {
		runes := []rune(transcript)
		half := transcriptMaxRunes / 2
		transcript = string(runes[:half]) + "\n\n[...middle truncated...]\n\n" + string(runes[len(runes)-half:])
	}
	prompt := fmt.Sprintf(
		"You are an assistant that summarizes short video calls, in English. Summarize the call below in a structured way:\n\n"+
			"## Summary (1 paragraph, 3-4 lines)\n"+
			"## Topics discussed (short bullets)\n"+
			"## Decisions and action items (if any)\n\n"+
			"Room: %s · Duration: %d minutes\n\n"+
			"Transcript (may contain speech recognition errors):\n\n%s",
		strings.TrimSpace(rec.RoomName), rec.DurationS/60, transcript)
	summary, err := callAnthropic(r.Context(), apiKey, prompt)
	if err != nil {
		http.Error(w, "anthropic: "+err.Error(), http.StatusBadGateway)
		return
	}
	_ = s.Recordings.SetSummary(user, id, summary)
	s.audit("videocall.summary_generated", user, id)
	writeJSONHTTP(w, map[string]string{"summary": summary, "cached": "0"})
}

func callAnthropic(ctx context.Context, apiKey, prompt string) (string, error) {
	body := map[string]any{
		"model":      anthropicModel,
		"max_tokens": summarizeMaxTokens,
		"messages": []map[string]any{
			{"role": "user", "content": prompt},
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", anthropicMessagesURL, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", anthropicVersion)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		return "", err
	}
	var sb strings.Builder
	for _, b := range out.Content {
		if b.Type == "text" {
			sb.WriteString(b.Text)
		}
	}
	if sb.Len() == 0 {
		return "", errors.New("empty response from Anthropic")
	}
	return sb.String(), nil
}
