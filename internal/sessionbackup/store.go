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
	OrigemManual     = "manual"
	OrigemAutomatica = "auto"
	OrigemAgendada   = "scheduled"
)

// idValido matches exactly the id format that [Store.Write] produces. It is
// the defence against directory traversal on every route that accepts an id
// from the user: the id becomes a file name, and an id with `../` a path.
var idValido = regexp.MustCompile(`^[0-9]+$`)

// IDValido reports whether an id coming from the user may become a file name.
func IDValido(id string) bool { return idValido.MatchString(id) }

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
// through [IDValido] when it came from the user.
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

// SessaoNoBackup is one session inside a backup, already summarized.
type SessaoNoBackup struct {
	Nome   string `json:"name"`
	Resumo string `json:"summary"`
	Linhas int    `json:"lines"`
}

// Meta is a backup's metadata — everything but the scrollback, which is the
// heavy part. A listing of 10 backups of 7 sessions would load megabytes of
// history just to draw 10 rows of a list.
type Meta struct {
	ID      string           `json:"id"`
	Criado  int64            `json:"created"`
	Origem  string           `json:"source,omitempty"`
	Sessoes []SessaoNoBackup `json:"sessions"`
	Bytes   int64            `json:"bytes"`
}

// List returns the user's backups, newest first.
func (s *Store) List(user string) []Meta {
	dir, err := s.Dir(user)
	if err != nil {
		return []Meta{}
	}
	entradas, _ := os.ReadDir(dir)
	out := make([]Meta, 0, len(entradas))
	for _, e := range entradas {
		id, ok := idDoArquivo(e)
		if !ok {
			continue
		}
		bk, err := s.Read(user, id)
		if err != nil {
			continue
		}
		var tamanho int64
		if info, err := e.Info(); err == nil {
			tamanho = info.Size()
		}
		sessoes := make([]SessaoNoBackup, 0, len(bk.Sessions))
		for _, sn := range bk.Sessions {
			sessoes = append(sessoes, SessaoNoBackup{
				Nome:   sn.Name,
				Resumo: Resumo(sn),
				Linhas: contaLinhas(sn),
			})
		}
		out = append(out, Meta{
			ID: bk.ID, Criado: bk.Created, Origem: bk.Source,
			Sessoes: sessoes, Bytes: tamanho,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Criado > out[j].Criado })
	return out
}

// Delete removes a whole backup or — with `sessao` filled in — only that one
// session inside it. A backup left with no sessions is deleted: a file with an
// empty list would show up in the listing promising to restore nothing.
func (s *Store) Delete(user, id, sessao string) error {
	dir, err := s.Dir(user)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, id+".json")

	if sessao == "" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}

	bk, err := s.Read(user, id)
	if err != nil {
		return err
	}
	alvo := ptysvc.SafeSessionName(sessao)
	mantidas := bk.Sessions[:0]
	for _, sn := range bk.Sessions {
		if ptysvc.SafeSessionName(sn.Name) != alvo {
			mantidas = append(mantidas, sn)
		}
	}
	bk.Sessions = mantidas
	if len(bk.Sessions) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return s.Write(user, bk)
}

// Prune keeps only the `keep` most recent backups of the user, IGNORING the
// per-session backups made by the scheduler ([OrigemAgendada]). Those have
// their own per-session retention ([Store.PruneSessao]); if the collector's
// global pruning counted them, a workday with many scheduled sessions would
// silently erase the history just created.
func (s *Store) Prune(user string, keep int) {
	s.podar(user, keep, func(bk ptysvc.Backup) bool { return bk.Source != OrigemAgendada })
}

// PruneSessao keeps only the `keep` most recent scheduled backups of ONE
// session. It touches neither bundles (manual/auto) nor backups of other
// sessions — each session has independent retention.
func (s *Store) PruneSessao(user, sessao string, keep int) {
	if keep <= 0 {
		return
	}
	alvo := ptysvc.SafeSessionName(sessao)
	s.podar(user, keep, func(bk ptysvc.Backup) bool {
		return bk.Source == OrigemAgendada &&
			len(bk.Sessions) == 1 &&
			ptysvc.SafeSessionName(bk.Sessions[0].Name) == alvo
	})
}

// podar removes the files that pass `elegivel` beyond the `keep` most recent
// ones. The order comes from the id (UnixNano), not from the mtime: the mtime
// changes when the file is rewritten — deleting a session from inside a backup
// rewrites it — and that would make an old backup look like the newest one.
func (s *Store) podar(user string, keep int, elegivel func(ptysvc.Backup) bool) {
	dir, err := s.Dir(user)
	if err != nil {
		return
	}
	entradas, _ := os.ReadDir(dir)
	type arquivo struct {
		nome string
		ts   int64
	}
	candidatos := make([]arquivo, 0, len(entradas))
	for _, e := range entradas {
		id, ok := idDoArquivo(e)
		if !ok {
			continue
		}
		bk, err := s.Read(user, id)
		if err != nil || !elegivel(bk) {
			continue
		}
		ts, _ := strconv.ParseInt(id, 10, 64)
		candidatos = append(candidatos, arquivo{nome: e.Name(), ts: ts})
	}
	if len(candidatos) <= keep {
		return
	}
	sort.Slice(candidatos, func(i, j int) bool { return candidatos[i].ts > candidatos[j].ts })
	for _, f := range candidatos[keep:] {
		_ = os.Remove(filepath.Join(dir, f.nome))
	}
}

func idDoArquivo(e os.DirEntry) (string, bool) {
	if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
		return "", false
	}
	return strings.TrimSuffix(e.Name(), ".json"), true
}

func contaLinhas(s ptysvc.SessionSnapshot) int {
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
func Resumo(s ptysvc.SessionSnapshot) string {
	var sb strings.Builder
	for _, w := range s.Windows {
		for _, p := range w.Panes {
			if p.Scrollback != "" {
				sb.WriteString(p.Scrollback)
				sb.WriteByte('\n')
			}
		}
	}
	manchete, corpo := ResumoDePainel(sb.String())
	out := manchete
	if out == "" && corpo != "" {
		linhas := strings.Split(strings.TrimRight(corpo, "\n"), "\n")
		out = strings.TrimSpace(linhas[len(linhas)-1])
	}
	if len(out) > 140 {
		out = out[:140] + "…"
	}
	return out
}

// ResumoDePainel reduces a capture-pane to something readable: it drops empty
// lines, separators and TUI borders, takes the last ~14 useful lines (the
// bottom of the screen is the most recent) and extracts a headline from the
// claude "recap:" line when there is one.
func ResumoDePainel(bruto string) (manchete, corpo string) {
	linhas := strings.Split(bruto, "\n")
	limpas := make([]string, 0, len(linhas))
	for _, ln := range linhas {
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
		if promptVazio(t) {
			continue
		}
		if i := strings.Index(t, "recap:"); i >= 0 {
			h := strings.TrimSpace(t[i+len("recap:"):])
			if len(h) > 240 {
				h = h[:240] + "…"
			}
			manchete = h
		}
		limpas = append(limpas, t)
	}
	if len(limpas) > 14 {
		limpas = limpas[len(limpas)-14:]
	}
	corpo = strings.Join(limpas, "\n")
	if len(corpo) > 1600 {
		corpo = corpo[len(corpo)-1600:]
	}
	return manchete, corpo
}

// promptVazio recognizes a line that is only the shell prompt, with no command
// after it. It covers the standard `user@host:path$` form (and `#` for root),
// which is what this server's sessions use.
//
// Deliberately conservative: it only discards when the `$`/`#` is the LAST
// character. A prompt with a command (`root@host:/opt# make build`) is
// informative, and is precisely what the summary exists to show.
func promptVazio(linha string) bool {
	if linha == "" {
		return false
	}
	fim := linha[len(linha)-1]
	if fim != '$' && fim != '#' {
		return false
	}
	arroba := strings.IndexByte(linha, '@')
	doisPontos := strings.IndexByte(linha, ':')
	return arroba > 0 && doisPontos > arroba
}
