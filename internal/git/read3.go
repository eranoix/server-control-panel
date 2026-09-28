package git

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"server-control-panel/internal/httpx"
)

type blameLine struct {
	Line     int    `json:"line"`
	Hash     string `json:"hash"`
	Short    string `json:"short"`
	Author   string `json:"author"`
	Email    string `json:"email"`
	Date     string `json:"date"`
	Summary  string `json:"summary"`
	Content  string `json:"content"`
	Boundary bool   `json:"boundary,omitempty"`
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
		httpx.WriteErr(w, http.StatusUnprocessableEntity, "blame unavailable (binary, missing, or no history)")
		return
	}
	lines := parseBlamePorcelain(res.Stdout)
	httpx.WriteJSON(w, map[string]any{"path": path, "lines": lines})
}

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
	res, err := run(r.Context(), repo.Path, "log", "--follow",
		"--max-count="+strconv.Itoa(limit), "--numstat",
		"--pretty=format:\x01%H%x00%an%x00%ae%x00%aI%x00%s", "--", path)
	if err != nil {
		httpx.WriteErr(w, http.StatusInternalServerError, "git log failed")
		return
	}
	httpx.WriteJSON(w, map[string]any{"path": path, "commits": parseFileLog(res.Stdout)})
}

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

	cres, _ := run(r.Context(), repo.Path, "log", "--left-right", "--date-order",
		"--max-count=500", "--pretty=format:%m%x00%H%x00%an%x00%aI%x00%s", a+"..."+b)
	type cmpCommit struct {
		Side    string `json:"side"`
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
