package sessionbackup

import (
	"os"
	"path/filepath"
	"testing"

	ptysvc "server-control-panel/internal/pty"
)

// The id validator is what blocks directory traversal on the restore and
// delete routes, which take the id straight from the client: the id becomes a
// file name. A `../` here is reading (and removing) an arbitrary file.
func TestValidID(t *testing.T) {
	valid := []string{"0", "1781093279761", "42"}
	invalid := []string{"", "../etc/passwd", "1781/../x", "abc", "12a", "1.2", "-1", "12 ", " 12"}
	for _, s := range valid {
		if !ValidID(s) {
			t.Errorf("IDValido refused the valid id %q", s)
		}
	}
	for _, s := range invalid {
		if ValidID(s) {
			t.Errorf("IDValido accepted the unsafe id %q", s)
		}
	}
}

func backup(id string, created int64, origin string, sessions ...string) ptysvc.Backup {
	bk := ptysvc.Backup{ID: id, Created: created, Source: origin}
	for _, name := range sessions {
		bk.Sessions = append(bk.Sessions, ptysvc.SessionSnapshot{
			Name: name,
			Windows: []ptysvc.WindowSnapshot{{
				Panes: []ptysvc.PaneSnapshot{{Scrollback: "linha um\nlinha dois\n"}},
			}},
		})
	}
	return bk
}

func store(t *testing.T) *Store {
	t.Helper()
	return New(t.TempDir())
}

func TestWriteListRead(t *testing.T) {
	s := store(t)
	if err := s.Write("sam", backup("100", 100, SourceManual, "web")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := s.Write("sam", backup("200", 200, SourceAuto, "web", "api")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	list := s.List("sam")
	if len(list) != 2 {
		t.Fatalf("expected 2 backups, got %d", len(list))
	}
	// Newest first: it is the order the screen shows, and it comes from here.
	if list[0].ID != "200" {
		t.Errorf("expected the newest first, got %q", list[0].ID)
	}
	if len(list[0].Sessions) != 2 {
		t.Errorf("expected 2 sessions in backup 200, got %d", len(list[0].Sessions))
	}
	if list[0].Sessions[0].Summary == "" {
		t.Error("the summary is what the list shows of each session; it came back empty")
	}
	if list[0].Bytes == 0 {
		t.Error("Bytes is the on-disk size shown on screen; it came back zero")
	}
}

// A user must never see, restore or delete another's backup: the paths are
// derived from the user name and cannot cross.
func TestOneUsersBackupDoesNotShowForAnother(t *testing.T) {
	s := store(t)
	if err := s.Write("sam", backup("100", 100, SourceManual, "web")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if list := s.List("teste"); len(list) != 0 {
		t.Fatalf("the backup leaked to another user: %d entries", len(list))
	}
	if _, err := s.Read("teste", "100"); err == nil {
		t.Fatal("a Read from another user should fail")
	}
}

// Deleting ONE session from inside a backup preserves the others — the backup
// is a bundle, and losing the whole bundle because of a single session would be
// destructive beyond what was asked.
func TestDeletingOneSessionKeepsTheRest(t *testing.T) {
	s := store(t)
	if err := s.Write("sam", backup("100", 100, SourceManual, "web", "api")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := s.Delete("sam", "100", "web"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	bk, err := s.Read("sam", "100")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(bk.Sessions) != 1 || bk.Sessions[0].Name != "api" {
		t.Fatalf("expected only the api session, got %+v", bk.Sessions)
	}
}

// A backup left with no sessions disappears: a file with an empty list would
// show up on screen promising to restore nothing.
func TestBackupWithoutSessionsIsDeleted(t *testing.T) {
	s := store(t)
	if err := s.Write("sam", backup("100", 100, SourceManual, "web")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := s.Delete("sam", "100", "web"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if list := s.List("sam"); len(list) != 0 {
		t.Fatalf("the empty backup stayed on the list: %+v", list)
	}
}

// The global pruning must NOT touch the scheduled backups: they have their own
// per-session retention, and a workday with many scheduled sessions would
// silently erase the history just created if both tracks shared one limit.
func TestGlobalPruneKeepsScheduled(t *testing.T) {
	s := store(t)
	for i := 1; i <= 5; i++ {
		id := string(rune('0'+i)) + "00"
		if err := s.Write("sam", backup(id, int64(i), SourceAuto, "web")); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if err := s.Write("sam", backup("900", 9, SourceScheduled, "web")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	s.Prune("sam", 2)

	viu := map[string]bool{}
	for _, m := range s.List("sam") {
		viu[m.ID] = true
	}
	if !viu["900"] {
		t.Error("the global prune deleted a scheduled backup")
	}
	if len(viu) != 3 { // 2 automatic ones + the scheduled one, untouched
		t.Errorf("expected 3 backups after the prune, got %d: %v", len(viu), viu)
	}
}

// The per-session retention looks only at its own session — it must not touch
// bundles nor scheduled backups of another session.
func TestPerSessionPruneTouchesOnlyItsSession(t *testing.T) {
	s := store(t)
	for i := 1; i <= 3; i++ {
		id := string(rune('0'+i)) + "00"
		if err := s.Write("sam", backup(id, int64(i), SourceScheduled, "web")); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if err := s.Write("sam", backup("700", 7, SourceScheduled, "api")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := s.Write("sam", backup("800", 8, SourceManual, "web")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	s.PruneSession("sam", "web", 1)

	viu := map[string]bool{}
	for _, m := range s.List("sam") {
		viu[m.ID] = true
	}
	if !viu["700"] {
		t.Error("the per-session prune deleted the scheduled backup of ANOTHER session")
	}
	if !viu["800"] {
		t.Error("the per-session prune deleted a manual backup")
	}
	if !viu["300"] {
		t.Error("the per-session prune should keep the session's most recent one")
	}
	if viu["100"] || viu["200"] {
		t.Error("the per-session prune did not remove the session's own old ones")
	}
}

// The write is atomic: the `.tmp` must never survive as if it were a backup,
// nor show up in the listing.
func TestWriteLeavesNoTempFileInListing(t *testing.T) {
	s := store(t)
	if err := s.Write("sam", backup("100", 100, SourceManual, "web")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	dir, err := s.Dir("sam")
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Fatalf("a temporary was left behind: %s", e.Name())
		}
	}
}

// An idle session is a pile of prompts with no command at all. Summarizing
// that spends two lines of the screen to say "it is idle" — which is what the
// absence of a summary already says for free.
func TestSummaryIgnoresEmptyPrompt(t *testing.T) {
	snap := ptysvc.SessionSnapshot{
		Windows: []ptysvc.WindowSnapshot{{
			Panes: []ptysvc.PaneSnapshot{{
				Scrollback: "root@srv:/opt/panel# \n" +
					"root@srv:/opt/panel# \n" +
					"sam@srv:~$ \n",
			}},
		}},
	}
	if got := Summary(snap); got != "" {
		t.Errorf("expected an empty summary for a stopped session, got %q", got)
	}
}

// The prompt WITH a command is exactly what the summary exists to show.
func TestSummaryKeepsPromptWithCommand(t *testing.T) {
	snap := ptysvc.SessionSnapshot{
		Windows: []ptysvc.WindowSnapshot{{
			Panes: []ptysvc.PaneSnapshot{{
				Scrollback: "root@srv:/opt/panel# \n" +
					"root@srv:/opt/panel# make build\n",
			}},
		}},
	}
	if got := Summary(snap); got != "root@srv:/opt/panel# make build" {
		t.Errorf("the command disappeared from the summary: %q", got)
	}
}

// A line ending in `$` without looking like a prompt (`total: 12$`) must not
// be discarded — the filter requires the user@host:path shape.
func TestSummaryDoesNotMistakeDollarForPrompt(t *testing.T) {
	snap := ptysvc.SessionSnapshot{
		Windows: []ptysvc.WindowSnapshot{{
			Panes: []ptysvc.PaneSnapshot{{Scrollback: "custo total em US$\n"}},
		}},
	}
	if got := Summary(snap); got != "custo total em US$" {
		t.Errorf("an ordinary line was discarded as a prompt: %q", got)
	}
}

// The claude headline (`※ recap: ...`) takes precedence over the last line:
// it is the sentence that describes the whole session.
func TestSummaryPrefersHeadline(t *testing.T) {
	snap := ptysvc.SessionSnapshot{
		Windows: []ptysvc.WindowSnapshot{{
			Panes: []ptysvc.PaneSnapshot{{
				Scrollback: "※ recap: consertando o teclado do app\n" +
					"root@srv:/opt# ls\n",
			}},
		}},
	}
	if got := Summary(snap); got != "consertando o teclado do app" {
		t.Errorf("expected the recap headline, got %q", got)
	}
}
