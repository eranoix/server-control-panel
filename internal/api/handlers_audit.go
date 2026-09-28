package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"server-control-panel/internal/auth"
)

func (r *Router) handleAuditTail(w http.ResponseWriter, req *http.Request) {
	if r.audit == nil {
		writeJSON(w, []any{})
		return
	}
	user := auth.UserFrom(req)
	if user == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	n := 100
	if v := req.URL.Query().Get("n"); v != "" {
		var k int
		if _, e := fmtSscan(v, &k); e == nil && k > 0 {
			n = k
		}
	}
	if n > 1000 {
		n = 1000
	}
	writeJSON(w, r.audit.TailForUser(n, user))
}

func (r *Router) handleAuditSearch(w http.ResponseWriter, req *http.Request) {
	if r.audit == nil {
		writeJSON(w, []any{})
		return
	}
	user := auth.UserFrom(req)
	if user == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	q := req.URL.Query()
	filter := auth.SearchFilter{
		TenantScope:    user,
		Action:         q.Get("action"),
		ActionPrefix:   q.Get("action_prefix"),
		TargetContains: q.Get("q"),
	}
	if v := q.Get("from"); v != "" {
		filter.From, _ = strconv.ParseInt(v, 10, 64)
	}
	if v := q.Get("to"); v != "" {
		filter.To, _ = strconv.ParseInt(v, 10, 64)
	}
	if v := q.Get("limit"); v != "" {
		filter.Limit, _ = strconv.Atoi(v)
	}
	maxLimit := 2000
	if q.Get("format") == "csv" {
		maxLimit = 10000
	}
	if filter.Limit <= 0 || filter.Limit > maxLimit {
		filter.Limit = maxLimit
	}

	events, err := r.audit.Search(filter)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}

	if q.Get("format") == "csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="audit.csv"`)
		w.Write([]byte("time,user,action,target,ip\n"))
		safe := func(s string) string {
			if len(s) > 0 {
				switch s[0] {
				case '=', '+', '-', '@', '\t', '\r':
					s = "'" + s
				}
			}
			return strings.ReplaceAll(s, `"`, `""`)
		}
		for _, e := range events {
			fmt.Fprintf(w, "%d,%q,%q,%q,%q\n",
				e.Time, safe(e.User), safe(e.Action), safe(e.Target), safe(e.IP))
		}
		return
	}
	writeJSON(w, events)
}

func (r *Router) handleAuditActions(w http.ResponseWriter, req *http.Request) {
	if r.audit == nil {
		writeJSON(w, []any{})
		return
	}
	user := auth.UserFrom(req)
	if user == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	writeJSON(w, sanitizeList(r.audit.DistinctActionsForUser(user)))
}
