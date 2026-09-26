// handlers_fs_browse.go — folder pickers for the scheduler UI.
//
// Two browsers, both primary-only:
//
//	GET /api/fs/browse?path=/abs        → subdirectories on the VPS host
//	GET /api/backup/remotes             → configured rclone remotes (cloud)
//	GET /api/backup/remote-browse?remote=&path= → folders inside a remote
//
// These power the "browse" buttons so the operator picks a backup
// destination by browsing instead of typing a path. rclone is the cloud
// abstraction (Google Drive, OneDrive, S3, Dropbox, … all via one mechanism);
// if it isn't installed the cloud picker degrades gracefully (installed:false).
//
// Primary-only is the right gate: primary already has full shell/exec on the
// host, so directory listing grants no new privilege.
package api

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"server-control-panel/internal/auth"
)

type fsDir struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// handleFSBrowse lists subdirectories of an absolute host path.
func (r *Router) handleFSBrowse(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.mustPrimary(w, req); !ok {
		return
	}
	path := strings.TrimSpace(req.URL.Query().Get("path"))
	if path == "" {
		path = "/"
	}
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) {
		writeErr(w, 400, "path must be absolute")
		return
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		writeErr(w, 400, "could not read "+path+": "+err.Error())
		return
	}
	dirs := make([]fsDir, 0, len(entries))
	for i, e := range entries {
		if i >= 2000 {
			break // cap — do not dump giant directories
		}
		name := e.Name()
		full := filepath.Join(path, name)
		isDir := e.IsDir()
		// Resolve symlinks that point at a directory.
		if !isDir && e.Type()&fs.ModeSymlink != 0 {
			if st, sErr := os.Stat(full); sErr == nil && st.IsDir() {
				isDir = true
			}
		}
		if isDir {
			dirs = append(dirs, fsDir{Name: name, Path: full})
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Name < dirs[j].Name })
	parent := filepath.Dir(path)
	writeJSON(w, map[string]any{"path": path, "parent": parent, "dirs": dirs})
}

// validRemoteName accepts an rclone remote name (alnum + _-).
func validRemoteName(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

// handleBackupRemotes lists configured rclone remotes (cloud destinations).
func (r *Router) handleBackupRemotes(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.mustPrimary(w, req); !ok {
		return
	}
	if _, err := exec.LookPath("rclone"); err != nil {
		writeJSON(w, map[string]any{"installed": false, "remotes": []string{}})
		return
	}
	out, err := exec.CommandContext(req.Context(), "rclone", "listremotes").Output()
	if err != nil {
		writeJSON(w, map[string]any{"installed": true, "remotes": []string{}, "error": err.Error()})
		return
	}
	remotes := make([]string, 0)
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line), ":"))
		if line != "" {
			remotes = append(remotes, line)
		}
	}
	sort.Strings(remotes)
	writeJSON(w, map[string]any{"installed": true, "remotes": remotes})
}

// rcloneConnectTypes is the allowlist of remote backends the in-app connect
// flow supports. Token-based ones use the headless OAuth recipe (the user runs
// `rclone authorize "<type>"` on a machine with a browser and pastes the token);
// s3 uses access key/secret.
var rcloneConnectTypes = map[string]bool{
	"drive": true, "onedrive": true, "dropbox": true, "box": true,
	"pcloud": true, "yandex": true, "b2": true, "s3": true,
	"sftp": true, "webdav": true, "ftp": true,
}

// handleBackupRemoteConnect creates an rclone remote from the UI (primary-only),
// so the operator never has to drop to a terminal. Inputs are passed to rclone
// as an argv slice (no shell), and --non-interactive keeps rclone from blocking
// on a prompt. For OAuth backends the token is obtained by the user out-of-band
// (rclone authorize on their own browser machine) and pasted here.
func (r *Router) handleBackupRemoteConnect(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.mustPrimary(w, req); !ok {
		return
	}
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	var body struct {
		Name      string `json:"name"`
		Type      string `json:"type"`
		Token     string `json:"token"`
		AccessKey string `json:"access_key"`
		Secret    string `json:"secret"`
		Region    string `json:"region"`
		Endpoint  string `json:"endpoint"`
		Provider  string `json:"provider"`
		Host      string `json:"host"`
		User      string `json:"user"`
		Pass      string `json:"pass"`
		Port      string `json:"port"`
		URL       string `json:"url"`
		KeyFile   string `json:"key_file"`
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	// Automatic in-app OAuth: take the token captured by the authorize session.
	if body.SessionID != "" {
		rcAuthMu.Lock()
		s, ok := rcAuthSessions[body.SessionID]
		if ok {
			if s.Token != "" {
				body.Token = s.Token
				if body.Type == "" {
					body.Type = s.Typ
				}
			}
			delete(rcAuthSessions, body.SessionID)
		}
		rcAuthMu.Unlock()
		if !ok || body.Token == "" {
			writeErr(w, 400, "login session not completed")
			return
		}
	}
	if !validRemoteName(body.Name) {
		writeErr(w, 400, "invalid remote name (use letters, digits, _ or -)")
		return
	}
	if !rcloneConnectTypes[body.Type] {
		writeErr(w, 400, "unsupported cloud type: "+body.Type)
		return
	}
	if _, err := exec.LookPath("rclone"); err != nil {
		writeErr(w, 400, "rclone is not installed on the host")
		return
	}
	// Reject any user value that could be smuggled as an rclone/cobra flag.
	// Defense-in-depth alongside the `--` end-of-options marker below: even
	// though this is primary-only (the caller already has shell), a value like
	// "--config=/x" must never be interpreted as a flag.
	for _, v := range []string{body.Provider, body.AccessKey, body.Secret, body.Region, body.Endpoint, body.Host, body.User, body.Pass, body.Port, body.URL, body.KeyFile, strings.TrimSpace(body.Token)} {
		if strings.HasPrefix(v, "-") {
			writeErr(w, 400, "invalid value (cannot start with '-')")
			return
		}
	}
	// Flags BEFORE `--`; all user-controlled positionals AFTER it, so cobra
	// stops flag parsing and treats name/type/key/values as literal. --obscure
	// makes rclone obfuscate password fields (sftp/webdav/ftp).
	preArgs := []string{"config", "create", "--non-interactive"}
	var args []string
	switch body.Type {
	case "s3":
		if body.AccessKey == "" || body.Secret == "" {
			writeErr(w, 400, "S3 requires access_key and secret")
			return
		}
		provider := body.Provider
		if provider == "" {
			provider = "Other"
		}
		args = append(args, "provider", provider, "access_key_id", body.AccessKey, "secret_access_key", body.Secret)
		if body.Region != "" {
			args = append(args, "region", body.Region)
		}
		if body.Endpoint != "" {
			args = append(args, "endpoint", body.Endpoint)
		}
	case "sftp":
		if body.Host == "" || body.User == "" {
			writeErr(w, 400, "SFTP requires host and user")
			return
		}
		preArgs = append(preArgs, "--obscure")
		args = append(args, "host", body.Host, "user", body.User)
		if body.Port != "" {
			args = append(args, "port", body.Port)
		}
		if body.Pass != "" {
			args = append(args, "pass", body.Pass)
		}
		if body.KeyFile != "" {
			args = append(args, "key_file", body.KeyFile)
		}
	case "ftp":
		if body.Host == "" || body.User == "" {
			writeErr(w, 400, "FTP requires host and user")
			return
		}
		preArgs = append(preArgs, "--obscure")
		args = append(args, "host", body.Host, "user", body.User)
		if body.Port != "" {
			args = append(args, "port", body.Port)
		}
		if body.Pass != "" {
			args = append(args, "pass", body.Pass)
		}
	case "webdav":
		if body.URL == "" {
			writeErr(w, 400, "WebDAV requires the URL")
			return
		}
		preArgs = append(preArgs, "--obscure")
		args = append(args, "url", body.URL, "vendor", "other")
		if body.User != "" {
			args = append(args, "user", body.User)
		}
		if body.Pass != "" {
			args = append(args, "pass", body.Pass)
		}
	default: // OAuth (drive/onedrive/dropbox/box/pcloud/yandex/b2): token colado
		if strings.TrimSpace(body.Token) == "" {
			writeErr(w, 400, "paste the token produced by: rclone authorize \""+body.Type+"\"")
			return
		}
		args = append(args, "token", strings.TrimSpace(body.Token), "config_is_local", "false")
	}
	full := append(preArgs, "--", body.Name, body.Type)
	full = append(full, args...)
	out, err := exec.CommandContext(req.Context(), "rclone", full...).CombinedOutput()
	if err != nil {
		writeErr(w, 400, "rclone config create failed: "+strings.TrimSpace(string(out)))
		return
	}
	r.auditEvent(req, auth.UserFrom(req), "backup.remote.connect", "name="+body.Name+" type="+body.Type)
	writeJSON(w, map[string]any{"ok": true, "name": body.Name})
}

// handleBackupRemoteBrowse lists folders inside an rclone remote path.
func (r *Router) handleBackupRemoteBrowse(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.mustPrimary(w, req); !ok {
		return
	}
	remote := strings.TrimSpace(req.URL.Query().Get("remote"))
	if !validRemoteName(remote) {
		writeErr(w, 400, "invalid remote")
		return
	}
	if _, err := exec.LookPath("rclone"); err != nil {
		writeErr(w, 400, "rclone is not installed on the host")
		return
	}
	rpath := strings.TrimSpace(req.URL.Query().Get("path"))
	rpath = strings.TrimPrefix(rpath, "/")
	target := remote + ":" + rpath
	out, err := exec.CommandContext(req.Context(), "rclone", "lsjson", target, "--dirs-only").Output()
	if err != nil {
		writeErr(w, 400, "rclone lsjson failed: "+err.Error())
		return
	}
	var listed []struct {
		Path  string `json:"Path"`
		Name  string `json:"Name"`
		IsDir bool   `json:"IsDir"`
	}
	if err := json.Unmarshal(out, &listed); err != nil {
		writeErr(w, 500, "unreadable rclone response")
		return
	}
	dirs := make([]fsDir, 0, len(listed))
	for _, e := range listed {
		if e.IsDir {
			full := strings.Trim(rpath+"/"+e.Name, "/")
			dirs = append(dirs, fsDir{Name: e.Name, Path: full})
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Name < dirs[j].Name })
	writeJSON(w, map[string]any{"remote": remote, "path": rpath, "dirs": dirs})
}

// ── automatic in-app OAuth ──
// Orchestrates `rclone authorize "<type>"`: the process prints a
// http://127.0.0.1:53682/auth?... URL and waits for the OAuth callback. The
// user opens that URL in vps-manager's OWN BROWSER (the persistent Chrome runs
// ON the VPS, so it reaches 127.0.0.1) and signs in; rclone captures the token
// and prints it. We capture the token and create the remote — all without a
// second browser.

type rcAuthSession struct {
	Typ     string
	URL     string
	Token   string
	Err     string
	Done    bool
	cmd     *exec.Cmd
	created time.Time
}

var (
	rcAuthMu       sync.Mutex
	rcAuthSessions = map[string]*rcAuthSession{}
	rcURLRe        = regexp.MustCompile(`https?://127\.0\.0\.1:\d+/auth\S*`)
)

func newRCAuthID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// rcAuthCleanupLocked removes sessions older than 15 min (the caller holds rcAuthMu).
func rcAuthCleanupLocked() {
	for id, s := range rcAuthSessions {
		if time.Since(s.created) > 15*time.Minute {
			if s.cmd != nil && s.cmd.Process != nil {
				_ = s.cmd.Process.Kill()
			}
			delete(rcAuthSessions, id)
		}
	}
}

// handleBackupRemoteAuthorize starts the automatic OAuth flow for a token-based type.
func (r *Router) handleBackupRemoteAuthorize(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.mustPrimary(w, req); !ok {
		return
	}
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	var body struct {
		Type string `json:"type"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	// Only makes sense for OAuth (token) backends; s3/sftp/ftp/webdav do not use this.
	switch body.Type {
	case "s3", "sftp", "ftp", "webdav", "":
		writeErr(w, 400, "this type does not use OAuth login")
		return
	}
	if !rcloneConnectTypes[body.Type] {
		writeErr(w, 400, "unsupported type: "+body.Type)
		return
	}
	if _, err := exec.LookPath("rclone"); err != nil {
		writeErr(w, 400, "rclone is not installed on the host")
		return
	}
	cmd := exec.Command("rclone", "authorize", body.Type, "--auth-no-open-browser")
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		writeErr(w, 500, "rclone authorize: "+err.Error())
		return
	}
	id := newRCAuthID()
	sess := &rcAuthSession{Typ: body.Type, cmd: cmd, created: time.Now()}
	rcAuthMu.Lock()
	rcAuthCleanupLocked()
	rcAuthSessions[id] = sess
	rcAuthMu.Unlock()

	// Wait for the process and CLOSE pw: without that the reader below never gets
	// EOF (pw is not an *os.File, so os/exec does not close it) and the goroutine
	// plus the process stayed stuck forever on every authorize.
	waitErr := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		_ = pw.Close()
		waitErr <- err
	}()

	// Read the output: capture the URL and the token (between the ---> / <---End paste markers).
	go func() {
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		capturing := false
		var tok strings.Builder
		for sc.Scan() {
			line := sc.Text()
			if m := rcURLRe.FindString(line); m != "" {
				rcAuthMu.Lock()
				sess.URL = m
				rcAuthMu.Unlock()
			}
			if strings.Contains(line, "--->") {
				capturing = true
				// take whatever follows the marker on the same line
				if i := strings.Index(line, "--->"); i >= 0 {
					rest := strings.TrimSpace(line[i+4:])
					if rest != "" && !strings.Contains(rest, "<---") {
						tok.WriteString(rest)
					}
				}
				continue
			}
			if strings.Contains(line, "<---") {
				capturing = false
				continue
			}
			if capturing {
				tok.WriteString(strings.TrimSpace(line))
			}
		}
		<-waitErr // processo ja encerrado (Wait feito acima)
		rcAuthMu.Lock()
		t := strings.TrimSpace(tok.String())
		if t != "" {
			sess.Token = t
		} else if sess.Err == "" {
			sess.Err = "could not capture the token (paste it manually instead)"
		}
		sess.Done = true
		rcAuthMu.Unlock()
	}()

	// Safety timeout: kill the process if nobody completes the sign-in.
	go func() {
		time.Sleep(5 * time.Minute)
		rcAuthMu.Lock()
		if !sess.Done && sess.cmd != nil && sess.cmd.Process != nil {
			_ = sess.cmd.Process.Kill()
			if sess.Err == "" {
				sess.Err = "timed out — start the connection again"
			}
		}
		rcAuthMu.Unlock()
	}()

	writeJSON(w, map[string]any{"id": id})
}

// handleBackupRemoteAuthorizeStatus reports the URL to open and whether the token arrived.
func (r *Router) handleBackupRemoteAuthorizeStatus(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.mustPrimary(w, req); !ok {
		return
	}
	id := strings.TrimSpace(req.URL.Query().Get("id"))
	rcAuthMu.Lock()
	s, ok := rcAuthSessions[id]
	var url, errMsg string
	ready := false
	if ok {
		url, errMsg, ready = s.URL, s.Err, s.Token != ""
	}
	rcAuthMu.Unlock()
	if !ok {
		writeErr(w, 404, "session not found")
		return
	}
	writeJSON(w, map[string]any{"url": url, "ready": ready, "error": errMsg})
}
