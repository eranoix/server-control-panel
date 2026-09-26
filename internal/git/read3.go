package git

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"server-control-panel/internal/httpx"
)

// read3.go — INSPECTION endpoints: blame by line, the history of a file and
// comparison between two refs. All primary-gated (through s.gate) and
// read-only (git shell-out, no writes).

// ---- GET /blame?repo=&path=&ref= ----

// blameLine is an annotated line: authorship + originating commit + content.
type blameLine struct {
	Line     int    `json:"line"`               // line number in the final file
	Hash     string `json:"hash"`               // full commit
	Short    string `json:"short"`              // short hash
	Author   string `json:"author"`             // author's name
	Email    string `json:"email"`              // author's e-mail
	Date     string `json:"date"`               // ISO (da author-time)
	Summary  string `json:"summary"`            // the commit's subject
	Content  string `json:"content"`            // the line's text
	Boundary bool   `json:"boundary,omitempty"` // boundary commit (the root of the window)
}

var blameHdrRe = regexp.MustCompile(`^([0-9a-f]{40}) (\d+) (\d+)(?: (\d+))?$`)

func (s *svc) handleBlame(w http.ResponseWriter, r *http.Request) {
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
	q := r.URL.Query()
	path := q.Get("path")
	if !validRelPath(path) {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid path")
		return
	}
	args := []string{"blame", "--line-porcelain"}
	if ref := q.Get("ref"); ref != "" {
		if !validRef(ref) {
			httpx.WriteErr(w, http.StatusBadRequest, "invalid ref")
			return
		}
		args = append(args, ref)
	}
	args = append(args, "--", path)
	res, err := run(r.Context(), repo.Path, args...)
	if err != nil {
		httpx.WriteErr(w, http.StatusInternalServerError, "git blame failed")
		return
	}
	if res.Code != 0 {
		// binary / nonexistent / history-less file → a clean 422
		httpx.WriteErr(w, http.StatusUnprocessableEntity, "blame unavailable (binary, missing, or no history)")
		return
	}
	lines := parseBlamePorcelain(res.Stdout)
	httpx.WriteJSON(w, map[string]any{"path": path, "lines": lines})
}

// parseBlamePorcelain interprets `git blame --line-porcelain`: each line of
// the file arrives as a header "<hash> <origLine> <finalLine> [grp]" followed
// by "key value" pairs (author, author-mail, author-time, summary…) and,
// finally, the content line prefixed by a TAB.
func parseBlamePorcelain(out string) []blameLine {
	res := []blameLine{}
	var cur blameLine
	have := false
	for _, ln := range strings.Split(out, "\n") {
		if strings.HasPrefix(ln, "\t") {
			if have {
				cur.Content = ln[1:]
				cur.Short = shortHash(cur.Hash)
				res = append(res, cur)
				have = false
			}
			continue
		}
		if m := blameHdrRe.FindStringSubmatch(ln); m != nil {
			cur = blameLine{Hash: m[1]}
			cur.Line, _ = strconv.Atoi(m[3])
			have = true
			continue
		}
		if !have {
			continue
		}
		key, val, _ := strings.Cut(ln, " ")
		switch key {
		case "author":
			cur.Author = val
		case "author-mail":
			cur.Email = strings.Trim(val, "<>")
		case "author-time":
			if sec, e := strconv.ParseInt(val, 10, 64); e == nil {
				cur.Date = time.Unix(sec, 0).UTC().Format(time.RFC3339)
			}
		case "summary":
			cur.Summary = val
		case "boundary":
			cur.Boundary = true
		}
	}
	return res
}

// ---- GET /filelog?repo=&path=&limit= ----

// fileLogEntry is a commit that touched a file (with that file's +/−).
type fileLogEntry struct {
	Hash    string `json:"hash"`
	Short   string `json:"short"`
	Author  string `json:"author"`
	Email   string `json:"email"`
	Date    string `json:"date"`
	Subject string `json:"subject"`
	Add     int    `json:"add"`
	Del     int    `json:"del"`
}

func (s *svc) handleFileLog(w http.ResponseWriter, r *http.Request) {
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
	q := r.URL.Query()
	path := q.Get("path")
	if !validRelPath(path) {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid path")
		return
	}
	limit := clampLimit(atoiDefault(q.Get("limit"), 100), 100)
	// --follow follows renames (it demands exactly 1 pathspec). numstat gives the
	// file's +/−; we separate commit-meta and numstat by NUL markers.
	res, err := run(r.Context(), repo.Path, "log", "--follow",
		"--max-count="+strconv.Itoa(limit), "--numstat",
		"--pretty=format:\x01%H%x00%an%x00%ae%x00%aI%x00%s", "--", path)
	if err != nil {
		httpx.WriteErr(w, http.StatusInternalServerError, "git log failed")
		return
	}
	httpx.WriteJSON(w, map[string]any{"path": path, "commits": parseFileLog(res.Stdout)})
}

// parseFileLog interprets the log with numstat: each commit starts with \x01
// and has its fields separated by NUL; the following lines (up to the next
// \x01) are numstat "<add>\t<del>\t<path>" — we sum the +/− (renames carry a
// path with "{a => b}", so we take only the numbers).
func parseFileLog(out string) []fileLogEntry {
	res := []fileLogEntry{}
	var cur *fileLogEntry
	flush := func() {
		if cur != nil {
			res = append(res, *cur)
			cur = nil
		}
	}
	for _, ln := range strings.Split(out, "\n") {
		if strings.HasPrefix(ln, "\x01") {
			flush()
			f := strings.Split(ln[1:], "\x00")
			if len(f) < 5 {
				continue
			}
			cur = &fileLogEntry{Hash: f[0], Short: shortHash(f[0]), Author: f[1], Email: f[2], Date: f[3], Subject: f[4]}
			continue
		}
		if cur == nil || ln == "" {
			continue
		}
		parts := strings.SplitN(ln, "\t", 3)
		if len(parts) < 3 {
			continue
		}
		if a, e := strconv.Atoi(parts[0]); e == nil {
			cur.Add += a
		}
		if d, e := strconv.Atoi(parts[1]); e == nil {
			cur.Del += d
		}
	}
	flush()
	return res
}

// ---- GET /compare?repo=&a=&b=&mode= ----

// handleCompare compares two refs: changed files (name-status + numstat) and
// the commits on each side. mode=threedot (a...b, the default) uses the
// merge-base (useful for "what B brings on top of A"); mode=twodot (a..b) is the direct difference.
func (s *svc) handleCompare(w http.ResponseWriter, r *http.Request) {
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
	q := r.URL.Query()
	a, b := q.Get("a"), q.Get("b")
	if !validRef(a) || !validRef(b) {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid refs")
		return
	}
	sep := "..."
	if q.Get("mode") == "twodot" {
		sep = ".."
	}
	rangeSpec := a + sep + b

	// Changed files (name-status + numstat) between the two.
	fres, _ := run(r.Context(), repo.Path, "diff", "--name-status", "-z", rangeSpec)
	files := parseNameStatusZ(fres.Stdout)
	nres, _ := run(r.Context(), repo.Path, "diff", "--numstat", "-z", rangeSpec)
	stats := parseNumstatZ(nres.Stdout)
	var totA, totD int
	for i := range files {
		if st, ok := stats[files[i].Path]; ok {
			files[i].Add, files[i].Del = st[0], st[1]
			totA += st[0]
			totD += st[1]
		}
	}

	// The commits on each side (always a...b, with a </> marker).
	cres, _ := run(r.Context(), repo.Path, "log", "--left-right", "--date-order",
		"--max-count=500", "--pretty=format:%m%x00%H%x00%an%x00%aI%x00%s", a+"..."+b)
	type cmpCommit struct {
		Side    string `json:"side"` // "a" (<) or "b" (>)
		Hash    string `json:"hash"`
		Short   string `json:"short"`
		Author  string `json:"author"`
		Date    string `json:"date"`
		Subject string `json:"subject"`
	}
	commits := []cmpCommit{}
	var aheadA, aheadB int
	for _, ln := range splitLines(cres.Stdout) {
		f := strings.Split(ln, "\x00")
		if len(f) < 5 {
			continue
		}
		side := "b"
		if f[0] == "<" {
			side = "a"
			aheadA++
		} else {
			aheadB++
		}
		commits = append(commits, cmpCommit{
			Side: side, Hash: f[1], Short: shortHash(f[1]), Author: f[2], Date: f[3], Subject: f[4],
		})
	}

	httpx.WriteJSON(w, map[string]any{
		"a": a, "b": b, "mode": q.Get("mode"),
		"files": files, "additions": totA, "deletions": totD,
		"commits": commits, "ahead_a": aheadA, "ahead_b": aheadB,
	})
}
