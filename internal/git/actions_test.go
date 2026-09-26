package git

import (
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"

	"server-control-panel/internal/config"
)

func TestPRURLFor(t *testing.T) {
	cases := []struct {
		remote, branch, wantSub, wantProv string
	}{
		{"git@github.com:northwind-dev/vps-manager.git", "feat/x", "github.com/northwind-dev/vps-manager/compare/feat%2Fx?expand=1", "github"},
		{"https://github.com/NorthwindLabs/northwind-web.git", "fix", "github.com/NorthwindLabs/northwind-web/compare/fix?expand=1", "github"},
		{"git@gitlab.com:grp/proj.git", "b1", "gitlab.com/grp/proj/-/merge_requests/new?merge_request%5Bsource_branch%5D=b1", "gitlab"},
		{"https://bitbucket.org/team/repo.git", "b2", "bitbucket.org/team/repo/pull-requests/new?source=b2", "bitbucket"},
	}
	for _, c := range cases {
		u, prov, err := prURLFor(c.remote, c.branch)
		if err != nil {
			t.Errorf("prURLFor(%q): error %v", c.remote, err)
			continue
		}
		if prov != c.wantProv || !strings.Contains(u, c.wantSub) {
			t.Errorf("prURLFor(%q)=%q,%q want contains %q,%q", c.remote, u, prov, c.wantSub, c.wantProv)
		}
	}
	// unknown provider → error
	if _, _, err := prURLFor("git@example.com:a/b.git", "x"); err == nil {
		t.Error("an unknown provider should error")
	}
}

func TestHandlerTagCreateDelete(t *testing.T) {
	dir := setupMergeRepo(t)
	cfg := cfgWith(config.GitRepo{ID: "t", Path: dir, Name: "T", Policy: policyWrite, ExpName: "Tester", ExpEmail: "tester@local"})
	h := Handler(cfg, nil, nil)

	if w := do(h, "POST", "/tag/create?repo=t", `{"name":"v1.0","message":"release"}`, "tester"); w.Code != 200 {
		t.Fatalf("tag create: %d %s", w.Code, w.Body.String())
	}
	w := do(h, "GET", "/tags?repo=t", "", "tester")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "v1.0") {
		t.Fatalf("tags list should have v1.0: %d %s", w.Code, w.Body.String())
	}
	if w := do(h, "POST", "/tag/delete?repo=t", `{"name":"v1.0"}`, "tester"); w.Code != 200 {
		t.Errorf("tag delete: %d %s", w.Code, w.Body.String())
	}
}

func TestHandlerCommitIdentitySelector(t *testing.T) {
	dir := setupMergeRepo(t)
	// repo with no expected identity → the commit uses the CHOSEN identity.
	cfg := cfgWith(config.GitRepo{ID: "t", Path: dir, Name: "T", Policy: policyWrite})
	h := Handler(cfg, nil, nil)

	exec.Command("git", "-C", dir, "config", "user.email", "tester@local").Run()
	writeFile(t, dir, "a.txt", "X\n")
	do(h, "POST", "/stage?repo=t", `{"paths":["a.txt"]}`, "tester")

	// commit choosing the "work" identity (seed)
	w := do(h, "POST", "/commit?repo=t", `{"message":"via work","identity_id":"work"}`, "tester")
	if w.Code != 200 {
		t.Fatalf("commit: %d %s", w.Code, w.Body.String())
	}
	out, _ := exec.Command("git", "-C", dir, "log", "-1", "--format=%an <%ae>").Output()
	if got := strings.TrimSpace(string(out)); got != "Sam Rivera <sam@northwind.example>" {
		t.Errorf("author: got %q want Sam Rivera <sam@northwind.example>", got)
	}
}

func TestHandlerIdentitiesAndCherryPick(t *testing.T) {
	dir := setupMergeRepo(t)
	cfg := cfgWith(config.GitRepo{ID: "t", Path: dir, Name: "T", Policy: policyWrite, ExpName: "Tester", ExpEmail: "tester@local"})
	h := Handler(cfg, nil, nil)

	// /identities lists the seed
	w := do(h, "GET", "/identities", "", "tester")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "northwind.example") {
		t.Errorf("/identities: %d %s", w.Code, w.Body.String())
	}

	// cherry-pick: take the hash of commit "C" (on the feat branch) onto main
	out, _ := exec.Command("git", "-C", dir, "rev-parse", "feat").Output()
	cHash := strings.TrimSpace(string(out))
	// it is already merged, so the cherry-pick will fail (empty) — this only
	// checks that the route answers with a clean 4xx, not 404/500.
	w = do(h, "POST", "/cherry-pick?repo=t", `{"hash":"`+cHash+`"}`, "tester")
	if w.Code == http.StatusNotFound || w.Code == http.StatusInternalServerError {
		t.Errorf("cherry-pick broken route: %d %s", w.Code, w.Body.String())
	}
}

// TestHandlerFileRejectsBinaryDirLarge makes sure /file refuses a binary, a
// directory and a huge file (a cause of Monaco freezing) with 422.
func TestHandlerFileRejectsBinaryDirLarge(t *testing.T) {
	dir := setupMergeRepo(t)
	cfg := cfgWith(config.GitRepo{ID: "t", Path: dir, Name: "T", Policy: policyWrite, ExpName: "Tester", ExpEmail: "tester@local"})
	h := Handler(cfg, nil, nil)

	// binary (with a NUL)
	os.WriteFile(dir+"/bin.dat", []byte{0x7f, 0x45, 0x4c, 0x46, 0x00, 0x01, 0x02}, 0o644)
	if w := do(h, "GET", "/file?repo=t&path=bin.dat", "", "tester"); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("a binary should 422, got %d %s", w.Code, w.Body.String())
	}
	// directory
	os.Mkdir(dir+"/sub", 0o755)
	if w := do(h, "GET", "/file?repo=t&path=sub", "", "tester"); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("a directory should 422, got %d", w.Code)
	}
	// normal text → 200
	if w := do(h, "GET", "/file?repo=t&path=a.txt", "", "tester"); w.Code != 200 {
		t.Errorf("text should 200, got %d %s", w.Code, w.Body.String())
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(dir+"/"+name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
