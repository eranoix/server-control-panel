// handlers_audit.go — audit HTTP handlers (tail/search/actions).
// Extracted from api.go to shrink the god file and clarify the subsystem's boundaries.
//
// The routes stay wired in NewRouter (api.go:463-465).
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
	// Cap to avoid a giant response. The backend still stores everything in
	// data/audit.log; the UI has search for specific queries.
	if n > 1000 {
		n = 1000
	}
	writeJSON(w, r.audit.TailForUser(n, user))
}

// handleAuditSearch streams audit entries matching a filter. Query params:
//
//	user, action, action_prefix, q (target contains), from, to, limit
//	format=json|csv (default json)
//
// Examples:
//
//	/api/audit/search?action=login.ok&limit=50
//	/api/audit/search?action_prefix=container.&from=1779000000
//	/api/audit/search?user=sam&format=csv
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
	// ?user=X is ignored silently (no error raised, so as not to advertise that
	// the filter exists). TenantScope is the only source of truth — it forces the
	// result to contain only events visible to the caller.
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
	// Defensive cap: queries with no limit, or with a huge one, degrade latency
	// and RAM. CSV (download) allows up to 10k for auditing; inline JSON 2k.
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
		// CSV/formula injection guard: Excel/LibreOffice interpret a cell
		// that starts with `=`, `+`, `-`, `@`, `\t`, or `\r` as a formula.
		// Attacker-controlled fields (audit `target`, `user`) could ship
		// =HYPERLINK(...) and pop a shell when opened. Prefix with single
		// quote to neutralise — visible in raw text, hidden in Excel.
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

// handleAuditActions returns the distinct action names seen recently. Lets
// the UI populate the filter dropdown.
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
	// Front end: <template x-for="a in auditActions" :key="a"> — an empty string
	// breaks Alpine, and so do dupes. DistinctActionsForUser filters by
	// VisibleToUser, so the dropdown does not list actions that only other profiles
	// fire (e.g. Sam's "videocall.create" disappears for Jordan).
	writeJSON(w, sanitizeList(r.audit.DistinctActionsForUser(user)))
}
