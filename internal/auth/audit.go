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

type Event struct {
	Time   int64  `json:"time"`
	User   string `json:"user"`
	Action string `json:"action"`
	Target string `json:"target"`
	IP     string `json:"ip"`
}

const (
	auditRingCap           = 1000
	auditMaxBytes    int64 = 50 * 1024 * 1024
	auditKeepRotated       = 10
)

type AuditLog struct {
	mu       sync.RWMutex
	path     string
	ring     []Event
	f        *os.File
	writeCnt int
	maxBytes int64
}

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

func (a *AuditLog) rotateIfNeeded() {
	if a.f == nil || a.maxBytes <= 0 {
		return
	}
	fi, err := a.f.Stat()
	if err != nil || fi.Size() < a.maxBytes {
		return
	}
	_ = a.f.Close()
	a.f = nil

	rotated := fmt.Sprintf("%s.%d", a.path, time.Now().Unix())
	if err := os.Rename(a.path, rotated); err != nil {
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
	sort.Strings(rotateds)
	for _, p := range rotateds[:len(rotateds)-auditKeepRotated] {
		_ = os.Remove(p)
	}
}

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
				_ = a.f.Sync()
			}
		}
		a.writeCnt++
		if a.writeCnt >= 256 {
			a.writeCnt = 0
			a.rotateIfNeeded()
		}
	}
}

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

type SearchFilter struct {
	User           string
	TenantScope    string
	Action         string
	ActionPrefix   string
	TargetContains string
	From, To       int64
	Limit          int
}

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
	if len(matches) > limit {
		matches = matches[len(matches)-limit:]
	}
	for i, j := 0, len(matches)-1; i < j; i, j = i+1, j-1 {
		matches[i], matches[j] = matches[j], matches[i]
	}
	return matches, nil
}

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
