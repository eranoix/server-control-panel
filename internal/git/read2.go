package git

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"server-control-panel/internal/httpx"
)

// ---- GET /tags ----

type tagInfo struct {
	Name      string `json:"name"`
	Target    string `json:"target"`
	Date      string `json:"date"`
	Subject   string `json:"subject"`
	Annotated bool   `json:"annotated"`
}

func (s *svc) handleTags(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.gate(w, r); !ok {
		return
	}
	if !requireGet(w, r) {
		return
	}
	repo, ok := s.repoParam(w, r)
	if !ok {
		return
	}
	res, err := run(r.Context(), repo.Path, "for-each-ref",
		"--sort=-creatordate",
		"--format=%(refname:short)%00%(objectname:short)%00%(creatordate:iso)%00%(contents:subject)%00%(objecttype)",
		"refs/tags")
	if err != nil || res.Code != 0 {
		httpx.WriteErr(w, http.StatusInternalServerError, "git tags failed")
		return
	}
	out := []tagInfo{}
	for _, ln := range splitLines(res.Stdout) {
		f := strings.Split(ln, "\x00")
		if len(f) < 5 {
			continue
		}
		out = append(out, tagInfo{
			Name: f[0], Target: f[1], Date: f[2], Subject: f[3],
			Annotated: f[4] == "tag",
		})
	}
	httpx.WriteJSON(w, map[string]any{"tags": out})
}

// ---- GET /stashes ----

type stashInfo struct {
	Ref     string `json:"ref"` // stash@{N}
	Hash    string `json:"hash"`
	Subject string `json:"subject"`
	Rel     string `json:"rel"`
}

func (s *svc) handleStashes(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.gate(w, r); !ok {
		return
	}
	if !requireGet(w, r) {
		return
	}
	repo, ok := s.repoParam(w, r)
	if !ok {
		return
	}
	res, err := run(r.Context(), repo.Path, "stash", "list",
		"--format=%gd%x00%H%x00%s%x00%cr")
	if err != nil {
		httpx.WriteErr(w, http.StatusInternalServerError, "git stash failed")
		return
	}
	out := []stashInfo{}
	for _, ln := range splitLines(res.Stdout) {
		f := strings.Split(ln, "\x00")
		if len(f) < 4 {
			continue
		}
		out = append(out, stashInfo{Ref: f[0], Hash: shortHash(f[1]), Subject: f[2], Rel: f[3]})
	}
	httpx.WriteJSON(w, map[string]any{"stashes": out})
}

// ---- GET /remotes ----

func (s *svc) handleRemotes(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.gate(w, r); !ok {
		return
	}
	if !requireGet(w, r) {
		return
	}
	repo, ok := s.repoParam(w, r)
	if !ok {
		return
	}
	res, err := run(r.Context(), repo.Path, "remote", "-v")
	if err != nil || res.Code != 0 {
		httpx.WriteJSON(w, map[string]any{"remotes": []any{}})
		return
	}
	type remote struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	seen := map[string]bool{}
	out := []remote{}
	for _, ln := range splitLines(res.Stdout) {
		fields := strings.Fields(ln)
		if len(fields) < 2 || seen[fields[0]] {
			continue
		}
		seen[fields[0]] = true
		out = append(out, remote{Name: fields[0], URL: fields[1]})
	}
	httpx.WriteJSON(w, map[string]any{"remotes": out})
}

// ---- GET /identities ----

func (s *svc) handleIdentities(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.gate(w, r); !ok {
		return
	}
	if !requireGet(w, r) {
		return
	}
	httpx.WriteJSON(w, map[string]any{"identities": effectiveIdentities(s.cfg)})
}

// ---- GET /pr-url ----

// handlePRURL builds the Pull/Merge Request creation URL from the origin
// remote and the branch — the Git Graph model (it opens the pre-filled form at
// the provider). It supports GitHub, GitLab and Bitbucket. No `gh`/API: it is
// only a navigation URL (the user authenticates in their own browser), so it
// does not violate the identity separation (no automated action on the account).
func (s *svc) handlePRURL(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.gate(w, r); !ok {
		return
	}
	if !requireGet(w, r) {
		return
	}
	repo, ok := s.repoParam(w, r)
	if !ok {
		return
	}
	branch := r.URL.Query().Get("branch")
	if branch == "" || !validRef(branch) {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid branch")
		return
	}
	res, err := run(r.Context(), repo.Path, "remote", "get-url", "origin")
	if err != nil || res.Code != 0 {
		httpx.WriteErr(w, http.StatusUnprocessableEntity, "repo has no origin remote")
		return
	}
	prURL, provider, perr := prURLFor(strings.TrimSpace(res.Stdout), branch)
	if perr != nil {
		httpx.WriteErr(w, http.StatusUnprocessableEntity, perr.Error())
		return
	}
	httpx.WriteJSON(w, map[string]any{"url": prURL, "provider": provider})
}

var remoteRe = regexp.MustCompile(`^(?:git@|https?://|ssh://git@)([^/:]+)[:/](.+?)(?:\.git)?/?$`)

// prURLFor translates (remoteURL, branch) into the provider's PR creation URL.
func prURLFor(remoteURL, branch string) (string, string, error) {
	m := remoteRe.FindStringSubmatch(remoteURL)
	if m == nil {
		return "", "", errMsg("unrecognized remote: " + remoteURL)
	}
	host := strings.ToLower(m[1])
	path := strings.TrimSuffix(m[2], "/")
	b := url.QueryEscape(branch)
	switch {
	case strings.Contains(host, "github"):
		return "https://github.com/" + path + "/compare/" + b + "?expand=1", "github", nil
	case strings.Contains(host, "gitlab"):
		return "https://gitlab.com/" + path + "/-/merge_requests/new?merge_request%5Bsource_branch%5D=" + b, "gitlab", nil
	case strings.Contains(host, "bitbucket"):
		return "https://bitbucket.org/" + path + "/pull-requests/new?source=" + b, "bitbucket", nil
	default:
		return "", "", errMsg("unsupported PR provider: " + host)
	}
}

type errMsg string

func (e errMsg) Error() string { return string(e) }

// splitLines breaks the output into non-empty lines.
func splitLines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
