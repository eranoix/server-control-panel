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

const (
	SourceManual    = "manual"
	SourceAuto      = "auto"
	SourceScheduled = "scheduled"
)

var validIDRe = regexp.MustCompile(`^[0-9]+$`)

func ValidID(id string) bool { return validIDRe.MatchString(id) }

type Store struct {
	dataDir string
}

func New(dataDir string) *Store { return &Store{dataDir: dataDir} }

func (s *Store) Dir(user string) (string, error) {
	dir := filepath.Join(s.dataDir, "users", user, "session-backups")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

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

type BackedUpSession struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
	Lines   int    `json:"lines"`
}

type Meta struct {
	ID       string            `json:"id"`
	Created  int64             `json:"created"`
	Source   string            `json:"source,omitempty"`
	Sessions []BackedUpSession `json:"sessions"`
	Bytes    int64             `json:"bytes"`
}

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

func (s *Store) Prune(user string, keep int) {
	s.prune(user, keep, func(bk ptysvc.Backup) bool { return bk.Source != SourceScheduled })
}

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

func PanelSummary(raw string) (headline, body string) {
	lines := strings.Split(raw, "\n")
	cleaned := make([]string, 0, len(lines))
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if t == "" {
			continue
		}
		if strings.TrimLeft(t, "─│╭╮╰╯═╗╔╝╚┌┐└┘├┤┬┴┼ ·•") == "" {
			continue
		}
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
