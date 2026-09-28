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

type jiraWorkWatcher struct {
	owner    string
	issueKey string
	session  string
	started  time.Time
	cancel   context.CancelFunc
}

const userPromptGlyph = "❯"

var completionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(can\s+close\s+(it|this)|mark\s+(it\s+|this\s+)?(as\s+)?(done|resolved|complete[d]?)|close\s+(the\s+)?(ticket|issue|card)|you\s+can\s+mark\s+it)\b`),
	regexp.MustCompile(`(?mi)^\s*/done\b`),
}

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

func (r *Router) runJiraWorkWatcher(ctx context.Context, w *jiraWorkWatcher) {
	const (
		pollEvery    = 6 * time.Second
		maxDuration  = 8 * time.Hour
		captureLines = 200
	)
	deadline := w.started.Add(maxDuration)
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
		r.applyJiraDoneFromWatcher(ctx, w)
		return
	}
}

func (r *Router) applyJiraDoneFromWatcher(ctx context.Context, w *jiraWorkWatcher) {
	cli, err := r.jiraClientForOwner(w.owner)
	if err != nil {
		return
	}
	from, to, terr := tryJiraTransition(ctx, cli, w.issueKey, "done")
	if terr != nil {
		r.auditEventBackground(w.owner, "jira.ai.work.done.skip",
			fmt.Sprintf("%s err=%v", w.issueKey, terr))
		return
	}
	body := fmt.Sprintf(
		"✅ *Auto-completion (server-control-panel)* — %s\n\n"+
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

func capturePaneTail(session string, lines int) string {
	return ptysvc.SessionTail(session, lines)
}

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

var _ = func() *jira.Client { return nil }

var _ sync.Mutex
