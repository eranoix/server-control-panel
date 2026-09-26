package git

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/config"
)

// gitCmd runs git in the test's dir; it fails the test on error.
func gitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE=2026-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2026-01-01T00:00:00Z")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// setupMergeRepo creates a repo with a merge (2 parents) and a branch, using
// the local identity tester@local.
func setupMergeRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git missing")
	}
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-q", "-b", "main")
	gitCmd(t, dir, "config", "user.name", "Tester")
	gitCmd(t, dir, "config", "user.email", "tester@local")
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.txt", "A\n")
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-q", "-m", "A")
	gitCmd(t, dir, "branch", "feat")
	gitCmd(t, dir, "checkout", "-q", "feat")
	write("c.txt", "C\n")
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-q", "-m", "C")
	gitCmd(t, dir, "checkout", "-q", "main")
	write("b.txt", "B\n")
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-q", "-m", "B")
	gitCmd(t, dir, "merge", "-q", "--no-ff", "-m", "M", "feat")
	return dir
}

// cfgWith assembles a config with Primary=tester and the given allowlist (it
// replaces the built-in seed with the test repos through a new Path — effectiveRepos merges).
func cfgWith(repos ...config.GitRepo) *config.Config {
	return &config.Config{Primary: "tester", GitRepos: repos}
}

func do(h http.Handler, method, target, body, user string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	if user != "" {
		r = r.WithContext(auth.WithUser(r.Context(), user))
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestHandlerReposAndGraph(t *testing.T) {
	dir := setupMergeRepo(t)
	cfg := cfgWith(config.GitRepo{ID: "t", Path: dir, Name: "Test", Policy: policyWrite,
		ExpName: "Tester", ExpEmail: "tester@local"})
	h := Handler(cfg, nil, nil)

	// non-primary → 403
	if w := do(h, "GET", "/repos", "", "intruder"); w.Code != http.StatusForbidden {
		t.Errorf("non-primary /repos: got %d want 403", w.Code)
	}
	// no auth → 401
	if w := do(h, "GET", "/repos", "", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("anon /repos: got %d want 401", w.Code)
	}

	// primary /repos → 200, repo present with identity ok
	w := do(h, "GET", "/repos", "", "tester")
	if w.Code != 200 {
		t.Fatalf("/repos: got %d", w.Code)
	}
	var rd struct {
		Repos []map[string]any `json:"repos"`
	}
	json.Unmarshal(w.Body.Bytes(), &rd)
	var found map[string]any
	for _, r := range rd.Repos {
		if r["id"] == "t" {
			found = r
		}
	}
	if found == nil {
		t.Fatalf("repo 't' missing from /repos: %s", w.Body.String())
	}
	if found["writable"] != true || found["identity_ok"] != true {
		t.Errorf("wrong repo flags: %+v", found)
	}

	// /graph → a merge produces ≥2 lanes
	w = do(h, "GET", "/graph?repo=t&limit=50", "", "tester")
	if w.Code != 200 {
		t.Fatalf("/graph: got %d %s", w.Code, w.Body.String())
	}
	var g Graph
	json.Unmarshal(w.Body.Bytes(), &g)
	if len(g.Commits) < 4 {
		t.Errorf("expected ≥4 commits (A,B,C,M), got %d", len(g.Commits))
	}
	if g.MaxLane < 1 {
		t.Errorf("a merge should generate ≥2 lanes live, max_lane=%d", g.MaxLane)
	}

	// repo outside the allowlist → 404
	if w := do(h, "GET", "/graph?repo=nope", "", "tester"); w.Code != 404 {
		t.Errorf("nonexistent repo: got %d want 404", w.Code)
	}
}

func TestHandlerCommitIdentity(t *testing.T) {
	dir := setupMergeRepo(t)
	cfg := cfgWith(config.GitRepo{ID: "t", Path: dir, Name: "Test", Policy: policyWrite,
		ExpName: "Tester", ExpEmail: "tester@local"})
	h := Handler(cfg, nil, nil)

	// make a change and stage it
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("A2\n"), 0o644)
	if w := do(h, "POST", "/stage?repo=t", `{"paths":["a.txt"]}`, "tester"); w.Code != 200 {
		t.Fatalf("/stage: got %d %s", w.Code, w.Body.String())
	}
	// commit with the expected identity → 200, correct author
	w := do(h, "POST", "/commit?repo=t", `{"message":"edit a"}`, "tester")
	if w.Code != 200 {
		t.Fatalf("/commit: got %d %s", w.Code, w.Body.String())
	}
	cmd := exec.Command("git", "-C", dir, "log", "-1", "--format=%an <%ae>")
	out, _ := cmd.Output()
	if got := strings.TrimSpace(string(out)); got != "Tester <tester@local>" {
		t.Errorf("commit author: got %q want Tester <tester@local>", got)
	}
}

// The user may CHOOSE the author identity. The choice is authoritative (the
// commit signs with it through -c); divergence from what the repo expects
// becomes identity_warn=true, NOT a block. (The old behavior of aborting with
// 409 was relaxed at the user's explicit request.)
func TestHandlerCommitIdentityWarn(t *testing.T) {
	dir := setupMergeRepo(t)
	// The repo expects tester@local; we will commit choosing "work" (it diverges).
	cfg := cfgWith(config.GitRepo{ID: "t", Path: dir, Name: "Test", Policy: policyWrite,
		ExpName: "Tester", ExpEmail: "tester@local"})
	h := Handler(cfg, nil, nil)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("A3\n"), 0o644)
	do(h, "POST", "/stage?repo=t", `{"paths":["a.txt"]}`, "tester")
	w := do(h, "POST", "/commit?repo=t", `{"message":"x","identity_id":"work"}`, "tester")
	if w.Code != 200 {
		t.Fatalf("commit should 200 (authoritative choice), got %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"identity_warn":true`) {
		t.Errorf("it should flag identity_warn=true when it diverges from the expected one: %s", w.Body.String())
	}
	out, _ := exec.Command("git", "-C", dir, "log", "-1", "--format=%ae").Output()
	if got := strings.TrimSpace(string(out)); got != "sam@northwind.example" {
		t.Errorf("the author should be the chosen identity: got %q", got)
	}
}

func TestHandlerReadOnlyBlocksWrite(t *testing.T) {
	dir := setupMergeRepo(t)
	cfg := cfgWith(config.GitRepo{ID: "ro", Path: dir, Name: "RO", Policy: policyReadOnly})
	h := Handler(cfg, nil, nil)
	// reading allowed
	if w := do(h, "GET", "/graph?repo=ro&limit=10", "", "tester"); w.Code != 200 {
		t.Errorf("a read-only graph should 200, got %d", w.Code)
	}
	// writing blocked
	if w := do(h, "POST", "/commit?repo=ro", `{"message":"x"}`, "tester"); w.Code != http.StatusForbidden {
		t.Errorf("a commit on read-only should 403, got %d", w.Code)
	}
	if w := do(h, "POST", "/write?repo=ro", `{"path":"a.txt","content":"x"}`, "tester"); w.Code != http.StatusForbidden {
		t.Errorf("a write on read-only should 403, got %d", w.Code)
	}
}

func TestHandlerStatusAndBranches(t *testing.T) {
	dir := setupMergeRepo(t)
	cfg := cfgWith(config.GitRepo{ID: "t", Path: dir, Name: "Test", Policy: policyWrite,
		ExpName: "Tester", ExpEmail: "tester@local"})
	h := Handler(cfg, nil, nil)

	os.WriteFile(filepath.Join(dir, "untrk.txt"), []byte("new\n"), 0o644)
	w := do(h, "GET", "/status?repo=t", "", "tester")
	if w.Code != 200 {
		t.Fatalf("/status: %d", w.Code)
	}
	var st struct {
		Branch  string        `json:"branch"`
		Entries []statusEntry `json:"entries"`
	}
	json.Unmarshal(w.Body.Bytes(), &st)
	if st.Branch != "main" {
		t.Errorf("branch: got %q want main", st.Branch)
	}
	foundUntracked := false
	for _, e := range st.Entries {
		if e.Path == "untrk.txt" && e.Untracked {
			foundUntracked = true
		}
	}
	if !foundUntracked {
		t.Errorf("untrk.txt should show up as untracked: %+v", st.Entries)
	}

	w = do(h, "GET", "/branches?repo=t", "", "tester")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "feat") {
		t.Errorf("/branches should list 'feat': %d %s", w.Code, w.Body.String())
	}
}
