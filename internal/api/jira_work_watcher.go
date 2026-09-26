// jira_work_watcher.go — watches the (dtach) sessions opened through "Work on
// it now" and detects when the user signals they are finished ("tá
// funcionando", "ficou pronto", and so on). On detection it applies the
// transition to Done in Jira and posts a comment marking the completion.
//
// Why polling instead of a hook inside Claude? Claude Code has no "the user
// sent a message" hook — and even if it did, we want to detect the user's
// intent in natural language, not an explicit command. The pty log
// (SessionTail) is the source of truth: it captures what is rendered, including
// the user's input before Enter (and the echo afterwards).
//
// The watcher's life:
//   - Start: called by handleJiraAIWork when "Work on it now" is clicked.
//   - Run: a goroutine polls the session's log (SessionTail) every 6s.
//   - Stop: when a completion phrase is detected (the happy path), or after 8h
//     of inactivity (timeout), or if the dtach session stops existing.
//
// Idempotency: calling Start again on the same session replaces the previous
// watcher. Useful to cover the reattach when vps-manager restarts.
package api

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/jira"
	ptysvc "server-control-panel/internal/pty"
)

// jiraWorkWatcher is the minimal state needed to follow a session.
type jiraWorkWatcher struct {
	owner    string
	issueKey string
	session  string
	started  time.Time
	cancel   context.CancelFunc
}

// userPromptGlyph is the prefix Claude Code's TUI puts on the line of the
// message SUBMITTED by the user ("❯ text"). The assistant's answers start with
// "●" and tool output with "⎿". The watcher may ONLY examine the user's lines —
// otherwise the assistant's own prose (which mentions "done", "marcar como
// concluído", "ficou pronto" all the time while explaining the work) would
// trigger the closure on its own. This was the root cause of the premature
// Dones.
const userPromptGlyph = "❯"

// completionPatterns: ONLY an EXPLICIT intent to close, and it is tested only
// against the lines the user typed (see matchesCompletion). Feedback phrases
// ("ficou bom", "tá funcionando", "funcionou") were REMOVED on purpose: they
// are praise or observation, not an order to close — and they were closing the
// ticket in the middle of the work. To close, the user says explicitly "pode
// fechar" / "marcar como done" / "/done".
var completionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(pode\s+fechar|pode\s+marcar|marcar?\s+(como\s+)?(done|conclu[ií]d[oa]|resolvido)|fechar\s+(o\s+)?(ticket|chamado|card)|pode\s+dar\s+done)\b`),
	// "/done" — an explicit shortcut
	regexp.MustCompile(`(?mi)^\s*/done\b`),
}

// startJiraWorkWatcher starts (or replaces) a session's watcher. Thread-safe.
// A silent no-op when r.secrets is nil (we then have no way to obtain a
// per-owner Jira client).
func (r *Router) startJiraWorkWatcher(owner, key, session string) {
	if r == nil || r.secrets == nil || owner == "" || key == "" || session == "" {
		return
	}
	r.jiraWorkWatchersMu.Lock()
	if r.jiraWorkWatchers == nil {
		r.jiraWorkWatchers = make(map[string]*jiraWorkWatcher)
	}
	if old, ok := r.jiraWorkWatchers[session]; ok {
		old.cancel()
		delete(r.jiraWorkWatchers, session)
	}
	ctx, cancel := context.WithCancel(context.Background())
	w := &jiraWorkWatcher{
		owner:    owner,
		issueKey: key,
		session:  session,
		started:  time.Now(),
		cancel:   cancel,
	}
	r.jiraWorkWatchers[session] = w
	r.jiraWorkWatchersMu.Unlock()
	go r.runJiraWorkWatcher(ctx, w)
}

// stopJiraWorkWatcher cancels and removes a session's watcher. Idempotent.
// Called when the final transition is applied or when the session dies.
func (r *Router) stopJiraWorkWatcher(session string) {
	if r == nil {
		return
	}
	r.jiraWorkWatchersMu.Lock()
	defer r.jiraWorkWatchersMu.Unlock()
	if w, ok := r.jiraWorkWatchers[session]; ok {
		w.cancel()
		delete(r.jiraWorkWatchers, session)
	}
}

// runJiraWorkWatcher is the watcher's loop. It blocks until it detects a
// completion phrase, the session dies, or the maximum timeout (8h) elapses.
// Transient errors (capture-pane failing for a moment) do not stop the watcher.
func (r *Router) runJiraWorkWatcher(ctx context.Context, w *jiraWorkWatcher) {
	const (
		pollEvery   = 6 * time.Second
		maxDuration = 8 * time.Hour
		// How many chars of scrollback to look at on each poll. Reads the visible
		// pane + 200 lines of history to cover the case of the user typing
		// several messages in a burst without us managing to see them all.
		captureLines = 200
	)
	deadline := w.started.Add(maxDuration)
	// lastTail is the "windowing" that avoids re-triggering on the same text:
	// we only examine the delta since the last poll.
	var lastTail string
	defer r.stopJiraWorkWatcher(w.session)

	tick := time.NewTicker(pollEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		if time.Now().After(deadline) {
			return
		}
		if !sessionExists(w.session) {
			return
		}
		tail := capturePaneTail(w.session, captureLines)
		if tail == "" {
			continue
		}
		// Delta-only check: take only the content that is new since the last poll.
		// If tail is a prefix of lastTail (the scrollback is unchanged), there is
		// nothing new. Otherwise, the difference is what we examine.
		delta := tail
		if lastTail != "" && strings.HasPrefix(tail, lastTail) {
			delta = tail[len(lastTail):]
		}
		lastTail = tail
		if strings.TrimSpace(delta) == "" {
			continue
		}
		if !matchesCompletion(delta) {
			continue
		}
		// A completion phrase was detected. Apply the transition to Done.
		// Best effort — if it fails (a workflow with no done category, or Jira
		// being down), the watcher shuts itself down anyway rather than keep
		// hammering.
		r.applyJiraDoneFromWatcher(ctx, w)
		return
	}
}

// applyJiraDoneFromWatcher applies the final transition and posts a comment.
// Idempotent; if the issue is already done, it just comments to confirm.
func (r *Router) applyJiraDoneFromWatcher(ctx context.Context, w *jiraWorkWatcher) {
	cli, err := r.jiraClientForOwner(w.owner)
	if err != nil {
		return
	}
	// Apply the transition to whatever status sits in the "done" category.
	from, to, terr := tryJiraTransition(ctx, cli, w.issueKey, "done")
	if terr != nil {
		// Audit the error, but do not block the comment (perhaps the user only
		// wants a note).
		r.auditEventBackground(w.owner, "jira.ai.work.done.skip",
			fmt.Sprintf("%s err=%v", w.issueKey, terr))
		return
	}
	body := fmt.Sprintf(
		"✅ *Auto-completion (vps-manager)* — %s\n\n"+
			"The watcher spotted a phrase in work session `%s` indicating the task is done and working. "+
			"Status moved automatically from **%s** to **%s**.",
		time.Now().UTC().Format("2006-01-02 15:04 UTC"),
		w.session, from, to,
	)
	if _, cerr := cli.AddComment(ctx, w.issueKey, body); cerr != nil {
		r.auditEventBackground(w.owner, "jira.ai.work.done.commentfail",
			fmt.Sprintf("%s err=%v", w.issueKey, cerr))
	}
	r.auditEventBackground(w.owner, "jira.ai.work.done",
		fmt.Sprintf("%s %s → %s (session=%s)", w.issueKey, from, to, w.session))
}

// userTypedLines extracts from the pane's text ONLY the lines the user typed
// (prefixed with "❯" in the TUI), discarding the assistant's answers ("●") and
// tool output ("⎿"). It returns the user's text, one line per message. Without
// this, the assistant's prose triggers the watcher.
func userTypedLines(paneText string) string {
	var b strings.Builder
	for _, ln := range strings.Split(paneText, "\n") {
		t := strings.TrimSpace(ln)
		if !strings.HasPrefix(t, userPromptGlyph) {
			continue
		}
		t = strings.TrimSpace(strings.TrimPrefix(t, userPromptGlyph))
		if t != "" {
			b.WriteString(t)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// matchesCompletion returns true if the USER (and only the user) gave an
// explicit order to close. It examines only the user's prompt lines.
func matchesCompletion(text string) bool {
	user := userTypedLines(text)
	if user == "" {
		return false
	}
	for _, re := range completionPatterns {
		if re.MatchString(user) {
			return true
		}
	}
	return false
}

// capturePaneTail captures the pane's last N lines (visible + scroll).
// Returns "" if the session is gone or the tail failed. Takes the last N lines
// (= N lines back from the top of the screen) + -E - (end = the last line).
func capturePaneTail(session string, lines int) string {
	// The engine is dtach: reading means tailing the pty log (session.go).
	return ptysvc.SessionTail(session, lines)
}

// auditEventBackground is the audit helper for use outside the HTTP context.
// It does not take an *http.Request because the watcher runs independently. It
// uses the owner's name directly to fill user_id in the log.
func (r *Router) auditEventBackground(owner, event, detail string) {
	if r == nil || r.audit == nil {
		return
	}
	r.audit.Append(auth.Event{
		User:   owner,
		Action: event,
		Target: detail,
	})
}

// shutdownJiraWorkWatchers cancels every active watcher. Called from the
// router's Close so goroutines do not leak in tests or on a graceful shutdown.
func (r *Router) shutdownJiraWorkWatchers() {
	if r == nil {
		return
	}
	r.jiraWorkWatchersMu.Lock()
	defer r.jiraWorkWatchersMu.Unlock()
	for _, w := range r.jiraWorkWatchers {
		w.cancel()
	}
	r.jiraWorkWatchers = nil
}

// Keeps a reference to the jira.Client type so go vet does not complain if a
// refactor removes the direct use — the jiraClientForOwner helper is what
// returns it, and it is used in applyJiraDoneFromWatcher above.
var _ = func() *jira.Client { return nil }

// Keeps sync.Mutex reachable even after field refactors.
var _ sync.Mutex
