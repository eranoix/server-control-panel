package auth

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Event is a single audit log entry.
type Event struct {
	Time   int64  `json:"time"`
	User   string `json:"user"`
	Action string `json:"action"`
	Target string `json:"target"`
	IP     string `json:"ip"`
}

const (
	auditRingCap = 1000
	// auditMaxBytes is the maximum size of the live audit.log before it rotates.
	// 50MB ~ 500k events (avg 100 bytes/line). On a server at 10 evt/s that is a
	// rotation roughly every 14h. Override via the env VPSM_AUDIT_MAX_BYTES.
	auditMaxBytes int64 = 50 * 1024 * 1024
	// auditKeepRotated is how many rotated .gz files to keep.
	auditKeepRotated = 10
)

// AuditLog is an append-only audit log with an in-memory ring buffer.
type AuditLog struct {
	mu       sync.RWMutex
	path     string
	ring     []Event
	f        *os.File
	writeCnt int   // counter to check rotation periodically (not on every write)
	maxBytes int64 // overridable for tests
}

// NewAuditLog loads an existing JSON-lines log (tailing the last 1000 entries)
// and opens an append handle for subsequent writes.
func NewAuditLog(path string) (*AuditLog, error) {
	a := &AuditLog{
		path:     path,
		ring:     make([]Event, 0, auditRingCap),
		maxBytes: auditMaxBytes,
	}

	if existing, err := os.Open(path); err == nil {
		scanner := bufio.NewScanner(existing)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			var e Event
			if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
				continue
			}
			if len(a.ring) == auditRingCap {
				a.ring = a.ring[1:]
			}
			a.ring = append(a.ring, e)
		}
		existing.Close()
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	a.f = f
	return a, nil
}

// rotateIfNeeded checks if audit.log exceeded maxBytes. If yes:
//  1. Renames audit.log → audit.log.<unix>
//  2. Gzip-compacts audit.log.<unix> → audit.log.<unix>.gz and removes the original
//  3. Deletes the oldest ones to keep at most auditKeepRotated .gz files
//  4. Reopens audit.log clean
//
// Called under a.mu.Lock() by Append() periodically (every 256 writes).
func (a *AuditLog) rotateIfNeeded() {
	if a.f == nil || a.maxBytes <= 0 {
		return
	}
	fi, err := a.f.Stat()
	if err != nil || fi.Size() < a.maxBytes {
		return
	}
	// Close the live handle, rename, compress, reopen.
	_ = a.f.Close()
	a.f = nil

	rotated := fmt.Sprintf("%s.%d", a.path, time.Now().Unix())
	if err := os.Rename(a.path, rotated); err != nil {
		// The rename failed — reopen the original and carry on. The next
		// Append will try to rotate again.
		if f, err2 := os.OpenFile(a.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600); err2 == nil {
			a.f = f
		}
		return
	}
	go a.compressAndPrune(rotated)

	f, err := os.OpenFile(a.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err == nil {
		a.f = f
	}
}

// compressAndPrune gzip-compacts the rotated file and removes the oldest ones.
// Runs in a goroutine so it does not block Append().
func (a *AuditLog) compressAndPrune(rotatedPath string) {
	in, err := os.Open(rotatedPath)
	if err != nil {
		return
	}
	defer in.Close()
	gzPath := rotatedPath + ".gz"
	out, err := os.OpenFile(gzPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return
	}
	gw := gzip.NewWriter(out)
	if _, err := io.Copy(gw, in); err != nil {
		_ = gw.Close()
		_ = out.Close()
		_ = os.Remove(gzPath)
		return
	}
	_ = gw.Close()
	_ = out.Close()
	_ = os.Remove(rotatedPath)

	// List the .gz files, keep the auditKeepRotated most recent, delete the rest.
	dir := filepath.Dir(a.path)
	base := filepath.Base(a.path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var rotateds []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, base+".") && strings.HasSuffix(name, ".gz") {
			rotateds = append(rotateds, filepath.Join(dir, name))
		}
	}
	if len(rotateds) <= auditKeepRotated {
		return
	}
	sort.Strings(rotateds) // oldest first (timestamp in lexical order)
	for _, p := range rotateds[:len(rotateds)-auditKeepRotated] {
		_ = os.Remove(p)
	}
}

// Append records an event in memory and on disk.
func (a *AuditLog) Append(e Event) {
	if e.Time == 0 {
		e.Time = time.Now().Unix()
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if len(a.ring) == auditRingCap {
		a.ring = a.ring[1:]
	}
	a.ring = append(a.ring, e)

	if a.f != nil {
		if b, err := json.Marshal(e); err == nil {
			b = append(b, '\n')
			if _, err := a.f.Write(b); err == nil {
				// Fsync so this survives crashes — the audit log is the record
				// of who did what, and losing events to an unflushed buffer
				// breaks forensics.
				_ = a.f.Sync()
			}
		}
		// Check for rotation every 256 writes — Stat() is cheap, but not cheap
		// enough to run on every write in a hot path (10+ evts/s).
		a.writeCnt++
		if a.writeCnt >= 256 {
			a.writeCnt = 0
			a.rotateIfNeeded()
		}
	}
}

// Tail returns the last n events, newest first.
func (a *AuditLog) Tail(n int) []Event {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if n <= 0 {
		return nil
	}
	if n > len(a.ring) {
		n = len(a.ring)
	}
	out := make([]Event, 0, n)
	for i := len(a.ring) - 1; i >= len(a.ring)-n; i-- {
		out = append(out, a.ring[i])
	}
	return out
}

// VisibleToUser is the single rule for whether an audit event is visible to a
// given account. An event shows up for <user> if: (a) it is theirs; (b) it is
// "system" (or empty, equivalent to system before normalisation); (c) it is a
// pre-login failure (login.fail, before the user is known). Do not compare for
// equality directly — keeping the rule here prevents drift between
// Tail/Search/DistinctActions.
func VisibleToUser(e Event, user string) bool {
	if user == "" {
		return false
	}
	if e.User == user {
		return true
	}
	if e.User == "" || e.User == "system" {
		return true
	}
	return false
}

// TailForUser is Tail filtered by VisibleToUser. It applies the filter over
// the whole ring and returns up to n events (newest first). Not optimised —
// the ring is in-memory and small (200 entries).
func (a *AuditLog) TailForUser(n int, user string) []Event {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if n <= 0 || user == "" {
		return nil
	}
	out := make([]Event, 0, n)
	for i := len(a.ring) - 1; i >= 0 && len(out) < n; i-- {
		if VisibleToUser(a.ring[i], user) {
			out = append(out, a.ring[i])
		}
	}
	return out
}

// DistinctActionsForUser is DistinctActions restricted to the events visible to
// the user — keeps other accounts' action names out of the UI dropdown.
func (a *AuditLog) DistinctActionsForUser(user string) []string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if user == "" {
		return nil
	}
	set := make(map[string]struct{}, 32)
	for _, e := range a.ring {
		if VisibleToUser(e, user) {
			set[e.Action] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// SearchFilter narrows a Search() call. Empty fields are ignored.
//
// Tenant isolation: set TenantScope to the user from the JWT; the filter then
// applies VisibleToUser to every event, letting through that user's own events
// plus system ones. User (exact match) is orthogonal — it refines within the
// scope, but never widens beyond it. The HTTP handler MUST set TenantScope; do
// not rely on User from the query string for isolation.
type SearchFilter struct {
	User           string // exact match (refine within the tenant scope)
	TenantScope    string // if != "", filters with VisibleToUser(e, TenantScope)
	Action         string // exact match (e.g. "login.ok")
	ActionPrefix   string // prefix match (e.g. "login.")
	TargetContains string // case-insensitive substring
	From, To       int64  // inclusive unix-seconds bounds; zero = open
	Limit          int    // default 500, max 5000
}

// Search streams the on-disk audit.log applying `f`, returning matches newest
// first. This is bounded by Limit (default 500, max 5000) to keep memory
// predictable. The scan order is forward (oldest → newest); we collect into
// a slice and reverse before returning so the caller gets newest first.
func (a *AuditLog) Search(f SearchFilter) ([]Event, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 500
	}
	if limit > 5000 {
		limit = 5000
	}

	a.mu.RLock()
	path := a.path
	a.mu.RUnlock()

	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()

	sc := bufio.NewScanner(file)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)

	target := strings.ToLower(f.TargetContains)
	var matches []Event
	for sc.Scan() {
		var e Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			continue
		}
		if f.TenantScope != "" && !VisibleToUser(e, f.TenantScope) {
			continue
		}
		if f.User != "" && e.User != f.User {
			continue
		}
		if f.Action != "" && e.Action != f.Action {
			continue
		}
		if f.ActionPrefix != "" && !strings.HasPrefix(e.Action, f.ActionPrefix) {
			continue
		}
		if target != "" && !strings.Contains(strings.ToLower(e.Target), target) {
			continue
		}
		if f.From > 0 && e.Time < f.From {
			continue
		}
		if f.To > 0 && e.Time > f.To {
			continue
		}
		matches = append(matches, e)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	// Trim from the FRONT to keep the most recent `limit` entries.
	if len(matches) > limit {
		matches = matches[len(matches)-limit:]
	}
	// Reverse for newest-first.
	for i, j := 0, len(matches)-1; i < j; i, j = i+1, j-1 {
		matches[i], matches[j] = matches[j], matches[i]
	}
	return matches, nil
}

// DistinctActions returns the set of unique Action strings seen in the ring,
// sorted. Lets the UI populate a dropdown of available filters.
func (a *AuditLog) DistinctActions() []string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	set := make(map[string]struct{}, 32)
	for _, e := range a.ring {
		set[e.Action] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
