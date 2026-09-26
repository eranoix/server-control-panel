// Package sessionbackup stores and reads terminal session backups.
//
// # Why this package exists
//
// All of this logic used to live in `*api.Router` methods, which tied it to
// the web panel: the phone BFF (`internal/mobilebff`) has no Router and must
// not have one — by design it is a thin shell over the domain services. As
// long as persistence was a Router method, the only way for the app to offer
// backup would be to duplicate the reading and writing of the same files, and
// two implementations of the same format diverge — it is only a matter of time.
//
// Deliberately left out: the HTTP handlers (ownership, auditing, response)
// stay in `internal/api` and in `internal/mobilebff`, each with its own rules.
// Only what both need to do IDENTICALLY lives here — where the file goes, how
// it is written, how it is read, how it is pruned.
//
// # The format
//
// A backup is an `<id>.json` file under `data/users/<user>/session-backups/`,
// with the `id` being the `UnixNano` of its creation. The id sorts by time
// without anyone having to open the file, and that is what the pruning uses.
package sessionbackup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	ptysvc "server-control-panel/internal/pty"
)

// Backup sources. They label the track the file belongs to, and that is what
// separates the retention domains so that one track never erases the other's.
const (
	SourceManual    = "manual"
	SourceAuto      = "auto"
	SourceScheduled = "scheduled"
)

// validIDRe matches exactly the id format that [Store.Write] produces. It is
// the defence against directory traversal on every route that accepts an id
// from the user: the id becomes a file name, and an id with `../` a path.
var validIDRe = regexp.MustCompile(`^[0-9]+$`)

// ValidID reports whether an id coming from the user may become a file name.
func ValidID(id string) bool { return validIDRe.MatchString(id) }

// Store is a server's backup folder. It keeps only the `dataDir` because the
// rest of the path is derived from the user — no per-request state.
type Store struct {
	dataDir string
}

func New(dataDir string) *Store { return &Store{dataDir: dataDir} }

// Dir returns (creating it if needed) the user's backup folder. Mode 0700: a
// terminal's scrollback usually contains everything the person typed,
// including what they should not have typed.
func (s *Store) Dir(user string) (string, error) {
	dir := filepath.Join(s.dataDir, "users", user, "session-backups")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// Write stores the backup atomically (temp + rename). Without the rename, a
// crash mid-write would leave a truncated JSON under a valid file name — and
// the listing would silently skip that backup forever after.
func (s *Store) Write(user string, bk ptysvc.Backup) error {
	dir, err := s.Dir(user)
	if err != nil {
		return err
	}
	data, err := json.Marshal(bk)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, bk.ID+".json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Read reads and deserializes a backup. The `id` must already have gone
// through [ValidID] when it came from the user.
func (s *Store) Read(user, id string) (ptysvc.Backup, error) {
	var bk ptysvc.Backup
	dir, err := s.Dir(user)
	if err != nil {
		return bk, err
	}
	data, err := os.ReadFile(filepath.Join(dir, id+".json"))
	if err != nil {
		return bk, err
	}
	if err := json.Unmarshal(data, &bk); err != nil {
		return bk, err
	}
	return bk, nil
}

// BackedUpSession is one session inside a backup, already summarized.
type BackedUpSession struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
	Lines   int    `json:"lines"`
}

// Meta is a backup's metadata — everything but the scrollback, which is the
// heavy part. A listing of 10 backups of 7 sessions would load megabytes of
// history just to draw 10 rows of a list.
type Meta struct {
	ID       string            `json:"id"`
	Created  int64             `json:"created"`
	Source   string            `json:"source,omitempty"`
	Sessions []BackedUpSession `json:"sessions"`
	Bytes    int64             `json:"bytes"`
}

// List returns the user's backups, newest first.
func (s *Store) List(user string) []Meta {
	dir, err := s.Dir(user)
	if err != nil {
		return []Meta{}
	}
	entries, _ := os.ReadDir(dir)
	out := make([]Meta, 0, len(entries))
	for _, e := range entries {
		id, ok := idFromFile(e)
		if !ok {
			continue
		}
		bk, err := s.Read(user, id)
		if err != nil {
			continue
		}
		var size int64
		if info, err := e.Info(); err == nil {
			size = info.Size()
		}
		sessions := make([]BackedUpSession, 0, len(bk.Sessions))
		for _, sn := range bk.Sessions {
			sessions = append(sessions, BackedUpSession{
				Name:    sn.Name,
				Summary: Summary(sn),
				Lines:   countLines(sn),
			})
		}
		out = append(out, Meta{
			ID: bk.ID, Created: bk.Created, Source: bk.Source,
			Sessions: sessions, Bytes: size,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created > out[j].Created })
	return out
}

// Delete removes a whole backup or — with `sessao` filled in — only that one
// session inside it. A backup left with no sessions is deleted: a file with an
// empty list would show up in the listing promising to restore nothing.
func (s *Store) Delete(user, id, session string) error {
	dir, err := s.Dir(user)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, id+".json")

	if session == "" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}

	bk, err := s.Read(user, id)
	if err != nil {
		return err
	}
	target := ptysvc.SafeSessionName(session)
	kept := bk.Sessions[:0]
	for _, sn := range bk.Sessions {
		if ptysvc.SafeSessionName(sn.Name) != target {
			kept = append(kept, sn)
		}
	}
	bk.Sessions = kept
	if len(bk.Sessions) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return s.Write(user, bk)
}

// Prune keeps only the `keep` most recent backups of the user, IGNORING the
// per-session backups made by the scheduler ([SourceScheduled]). Those have
// their own per-session retention ([Store.PruneSession]); if the collector's
// global pruning counted them, a workday with many scheduled sessions would
// silently erase the history just created.
func (s *Store) Prune(user string, keep int) {
	s.prune(user, keep, func(bk ptysvc.Backup) bool { return bk.Source != SourceScheduled })
}

// PruneSession keeps only the `keep` most recent scheduled backups of ONE
// session. It touches neither bundles (manual/auto) nor backups of other
// sessions — each session has independent retention.
func (s *Store) PruneSession(user, session string, keep int) {
	if keep <= 0 {
		return
	}
	target := ptysvc.SafeSessionName(session)
	s.prune(user, keep, func(bk ptysvc.Backup) bool {
		return bk.Source == SourceScheduled &&
			len(bk.Sessions) == 1 &&
			ptysvc.SafeSessionName(bk.Sessions[0].Name) == target
	})
}

// podar removes the files that pass `elegivel` beyond the `keep` most recent
// ones. The order comes from the id (UnixNano), not from the mtime: the mtime
// changes when the file is rewritten — deleting a session from inside a backup
// rewrites it — and that would make an old backup look like the newest one.
func (s *Store) prune(user string, keep int, eligible func(ptysvc.Backup) bool) {
	dir, err := s.Dir(user)
	if err != nil {
		return
	}
	entries, _ := os.ReadDir(dir)
	type file struct {
		name string
		ts   int64
	}
	candidates := make([]file, 0, len(entries))
	for _, e := range entries {
		id, ok := idFromFile(e)
		if !ok {
			continue
		}
		bk, err := s.Read(user, id)
		if err != nil || !eligible(bk) {
			continue
		}
		ts, _ := strconv.ParseInt(id, 10, 64)
		candidates = append(candidates, file{name: e.Name(), ts: ts})
	}
	if len(candidates) <= keep {
		return
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].ts > candidates[j].ts })
	for _, f := range candidates[keep:] {
		_ = os.Remove(filepath.Join(dir, f.name))
	}
}

func idFromFile(e os.DirEntry) (string, bool) {
	if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
		return "", false
	}
	return strings.TrimSuffix(e.Name(), ".json"), true
}

func countLines(s ptysvc.SessionSnapshot) int {
	total := 0
	for _, w := range s.Windows {
		for _, p := range w.Panes {
			if p.Scrollback == "" {
				continue
			}
			total += strings.Count(p.Scrollback, "\n") + 1
		}
	}
	return total
}

// Resumo returns ONE line saying what the session is about, so the listing can
// show "what this backup was of" without opening anything.
func Summary(s ptysvc.SessionSnapshot) string {
	var sb strings.Builder
	for _, w := range s.Windows {
		for _, p := range w.Panes {
			if p.Scrollback != "" {
				sb.WriteString(p.Scrollback)
				sb.WriteByte('\n')
			}
		}
	}
	headline, body := PanelSummary(sb.String())
	out := headline
	if out == "" && body != "" {
		lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
		out = strings.TrimSpace(lines[len(lines)-1])
	}
	if len(out) > 140 {
		out = out[:140] + "…"
	}
	return out
}

// PanelSummary reduces a capture-pane to something readable: it drops empty
// lines, separators and TUI borders, takes the last ~14 useful lines (the
// bottom of the screen is the most recent) and extracts a headline from the
// claude "recap:" line when there is one.
func PanelSummary(raw string) (headline, body string) {
	lines := strings.Split(raw, "\n")
	cleaned := make([]string, 0, len(lines))
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if t == "" {
			continue
		}
		// Lines made only of TUI borders/separators say nothing.
		if strings.TrimLeft(t, "─│╭╮╰╯═╗╔╝╚┌┐└┘├┤┬┴┼ ·•") == "" {
			continue
		}
		// Nor an empty prompt. An idle session is a pile of
		// `root@host:/dir#` with nothing after it, and a summary made only of that
		// spends two lines of the screen to say "this session is idle" — which is
		// what the ABSENCE of a summary already says, for free.
		if emptyPrompt(t) {
			continue
		}
		if i := strings.Index(t, "recap:"); i >= 0 {
			h := strings.TrimSpace(t[i+len("recap:"):])
			if len(h) > 240 {
				h = h[:240] + "…"
			}
			headline = h
		}
		cleaned = append(cleaned, t)
	}
	if len(cleaned) > 14 {
		cleaned = cleaned[len(cleaned)-14:]
	}
	body = strings.Join(cleaned, "\n")
	if len(body) > 1600 {
		body = body[len(body)-1600:]
	}
	return headline, body
}

// emptyPrompt recognizes a line that is only the shell prompt, with no command
// after it. It covers the standard `user@host:path$` form (and `#` for root),
// which is what this server's sessions use.
//
// Deliberately conservative: it only discards when the `$`/`#` is the LAST
// character. A prompt with a command (`root@host:/opt# make build`) is
// informative, and is precisely what the summary exists to show.
func emptyPrompt(line string) bool {
	if line == "" {
		return false
	}
	done := line[len(line)-1]
	if done != '$' && done != '#' {
		return false
	}
	atIdx := strings.IndexByte(line, '@')
	colonIdx := strings.IndexByte(line, ':')
	return atIdx > 0 && colonIdx > atIdx
}
