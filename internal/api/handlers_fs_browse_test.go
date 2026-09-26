package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"server-control-panel/internal/config"
)

// handleFSBrowse lists only subdirectories, sets parent, and is primary-gated.
func TestFSBrowse(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"alpha", "beta"} {
		if err := os.Mkdir(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "afile.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &Router{cfg: &config.Config{Primary: "sam"}}

	// non-primary → 403
	rec := httptest.NewRecorder()
	r.handleFSBrowse(rec, schedReq(http.MethodGet, "/api/fs/browse?path="+root, "jordan", ""))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-primary browse: status=%d want 403", rec.Code)
	}

	// primary → lists the two dirs (not the file), parent set
	rec = httptest.NewRecorder()
	r.handleFSBrowse(rec, schedReq(http.MethodGet, "/api/fs/browse?path="+root, "sam", ""))
	if rec.Code != 200 {
		t.Fatalf("primary browse: status=%d body=%q", rec.Code, rec.Body.String())
	}
	var out struct {
		Path   string  `json:"path"`
		Parent string  `json:"parent"`
		Dirs   []fsDir `json:"dirs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Dirs) != 2 || out.Dirs[0].Name != "alpha" || out.Dirs[1].Name != "beta" {
		t.Fatalf("dirs = %+v, want [alpha beta] sorted, no file", out.Dirs)
	}
	if out.Parent != filepath.Dir(root) {
		t.Errorf("parent = %q, want %q", out.Parent, filepath.Dir(root))
	}

	// relative path rejected
	rec = httptest.NewRecorder()
	r.handleFSBrowse(rec, schedReq(http.MethodGet, "/api/fs/browse?path=relative/x", "sam", ""))
	if rec.Code != 400 {
		t.Errorf("relative path: status=%d want 400", rec.Code)
	}
}

// remote-connect validates name/type/permission before touching rclone.
func TestBackupRemoteConnectValidation(t *testing.T) {
	r := &Router{cfg: &config.Config{Primary: "sam"}}

	// non-primary → 403
	rec := httptest.NewRecorder()
	r.handleBackupRemoteConnect(rec, schedReq(http.MethodPost, "/api/backup/remote-connect", "jordan", `{"name":"g","type":"drive","token":"x"}`))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-primary connect: status=%d want 403", rec.Code)
	}
	// invalid name → 400
	rec = httptest.NewRecorder()
	r.handleBackupRemoteConnect(rec, schedReq(http.MethodPost, "/api/backup/remote-connect", "sam", `{"name":"bad name","type":"drive","token":"x"}`))
	if rec.Code != 400 {
		t.Errorf("invalid name: status=%d want 400", rec.Code)
	}
	// unsupported type → 400
	rec = httptest.NewRecorder()
	r.handleBackupRemoteConnect(rec, schedReq(http.MethodPost, "/api/backup/remote-connect", "sam", `{"name":"x","type":"ftp_weird","token":"x"}`))
	if rec.Code != 400 {
		t.Errorf("unsupported type: status=%d want 400", rec.Code)
	}
	// flag-smuggling value (leading '-') → 400 (argument injection guard)
	rec = httptest.NewRecorder()
	r.handleBackupRemoteConnect(rec, schedReq(http.MethodPost, "/api/backup/remote-connect", "sam", `{"name":"g","type":"drive","token":"--config=/etc/x"}`))
	if rec.Code != 400 {
		t.Errorf("flag-smuggling token: status=%d want 400", rec.Code)
	}
}

func TestValidRemoteName(t *testing.T) {
	for _, s := range []string{"gdrive", "one_drive", "s3-backup", "Rmt1"} {
		if !validRemoteName(s) {
			t.Errorf("expected valid remote: %q", s)
		}
	}
	for _, s := range []string{"", "has space", "a:b", "a/b", "a.b"} {
		if validRemoteName(s) {
			t.Errorf("expected invalid remote: %q", s)
		}
	}
}
