// Package todos is the maintenance-checklist backend for vps-manager.
//
// Each user owns a `<DataDir>/users/<u>/todos.json` file. The store
// supports one-shot TODOs (with a due date) and recurring TODOs (which
// re-schedule themselves the moment they're marked done).
//
// A long-running Watcher goroutine recomputes "overdue / due-soon" buckets
// on a 6h tick. There's no in-memory cache — every read/write reloads from
// disk so the JSON file stays the single source of truth (cheap; small).
//
// Auto-seed: the first time a user lands on the page with no TODOs, the
// HTTP handler will call SuggestSeed to build candidates from real host
// state (expiring TLS certs, last apt update, secrets older than 90 days).
package todos

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Category groups TODOs in the UI. Open-ended ("custom") for whatever
// the operator throws at it; predefined ones unlock auto-seed.
type Category string

const (
	CatSSLRenewal     Category = "ssl_renewal"
	CatSecretRotation Category = "secret_rotation"
	CatAptUpgrade     Category = "apt_upgrade"
	CatBackupCheck    Category = "backup_check"
	CatRebootDue      Category = "reboot_due"
	CatCustom         Category = "custom"
)

func ValidCategory(c Category) bool {
	switch c {
	case CatSSLRenewal, CatSecretRotation, CatAptUpgrade,
		CatBackupCheck, CatRebootDue, CatCustom:
		return true
	}
	return false
}

// Status is the current lifecycle bucket of a TODO. A done one-shot stays
// done forever; a done recurring rolls Due forward by IntervalDays and goes
// back to pending atomically.
type Status string

const (
	StatusPending Status = "pending"
	StatusDone    Status = "done"
	StatusSnoozed Status = "snoozed"
)

// Todo is the on-disk record. Times are unix sec UTC.
type Todo struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Notes        string   `json:"notes,omitempty"`
	Category     Category `json:"category"`
	Status       Status   `json:"status"`
	Due          int64    `json:"due,omitempty"`           // unix sec; 0 = no deadline
	IntervalDays int      `json:"interval_days,omitempty"` // >0 = recurring
	SnoozeUntil  int64    `json:"snooze_until,omitempty"`
	LastDone     int64    `json:"last_done,omitempty"`
	DoneCount    int      `json:"done_count,omitempty"`
	NotifyWA     bool     `json:"notify_wa,omitempty"` // notificar via WhatsApp quando vencer
	Created      int64    `json:"created"`
	Updated      int64    `json:"updated"`
}

// Bucket classifies a Todo by urgency for the UI kanban.
type Bucket string

const (
	BucketOverdue Bucket = "overdue"
	BucketWeek    Bucket = "week"
	BucketMonth   Bucket = "month"
	BucketFuture  Bucket = "future"
	BucketDone    Bucket = "done"
)

// Summary is the cheap aggregate the topbar badge consumes.
type Summary struct {
	Overdue int `json:"overdue"`
	Week    int `json:"week"`
	Month   int `json:"month"`
	Future  int `json:"future"`
	Done    int `json:"done"`
}

var (
	ErrNotFound = errors.New("todo not found")
	ErrBadInput = errors.New("invalid input")
)

// Store is the per-user persistence layer. Construct with NewStore using
// the user's `<Paths.Root>/todos.json` path.
type Store struct {
	path string
	mu   sync.Mutex
}

func NewStore(userRoot string) *Store {
	return &Store{path: filepath.Join(userRoot, "todos.json")}
}

func (s *Store) Path() string { return s.path }

func (s *Store) load() ([]Todo, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Todo
	if len(data) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("todos.json corrupt: %w", err)
	}
	return out, nil
}

// save atomically replaces the file: write tmp + rename, with a .bak of
// the previous version next to it.
func (s *Store) save(list []Todo) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	// fsync before the rename — durability after a crash.
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	// Atomic write: tmp→path (single POSIX rename). Old pattern moved
	// path→bak first, opening a crash window where no file existed.
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	// Best-effort .bak via copy after the rename (never blocks success).
	if data2, err := os.ReadFile(s.path); err == nil {
		_ = os.WriteFile(s.path+".bak", data2, 0o600)
	}
	return nil
}

// List returns every TODO sorted by (status pending first, then due asc).
func (s *Store) List() ([]Todo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out, err := s.load()
	if err != nil {
		return nil, err
	}
	sortTodos(out)
	return out, nil
}

func sortTodos(t []Todo) {
	sort.SliceStable(t, func(i, j int) bool {
		// Pending+snoozed before done; within group due asc; 0-due last.
		gi := groupKey(t[i])
		gj := groupKey(t[j])
		if gi != gj {
			return gi < gj
		}
		di, dj := t[i].Due, t[j].Due
		if di == 0 && dj == 0 {
			return t[i].Created < t[j].Created
		}
		if di == 0 {
			return false
		}
		if dj == 0 {
			return true
		}
		return di < dj
	})
}

func groupKey(t Todo) int {
	switch t.Status {
	case StatusPending:
		return 0
	case StatusSnoozed:
		return 1
	case StatusDone:
		return 2
	}
	return 3
}

// Create inserts a new TODO and returns the saved copy with ID + timestamps.
func (s *Store) Create(in Todo) (*Todo, error) {
	if strings.TrimSpace(in.Title) == "" {
		return nil, fmt.Errorf("%w: title empty", ErrBadInput)
	}
	if !ValidCategory(in.Category) {
		in.Category = CatCustom
	}
	if in.IntervalDays < 0 {
		return nil, fmt.Errorf("%w: interval_days < 0", ErrBadInput)
	}
	if in.Status == "" {
		in.Status = StatusPending
	}
	now := time.Now().Unix()
	in.ID = newID(now)
	in.Created = now
	in.Updated = now
	if in.IntervalDays > 0 && in.Due == 0 {
		in.Due = now + int64(in.IntervalDays)*86400
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.load()
	if err != nil {
		return nil, err
	}
	list = append(list, in)
	if err := s.save(list); err != nil {
		return nil, err
	}
	return &in, nil
}

// Update overwrites mutable fields on an existing TODO. Status changes go
// through Done/Snooze — Update is for title/notes/due/interval/notify edits.
func (s *Store) Update(id string, patch Todo) (*Todo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.load()
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].ID != id {
			continue
		}
		if strings.TrimSpace(patch.Title) != "" {
			list[i].Title = patch.Title
		}
		list[i].Notes = patch.Notes
		if ValidCategory(patch.Category) {
			list[i].Category = patch.Category
		}
		list[i].Due = patch.Due
		list[i].IntervalDays = patch.IntervalDays
		list[i].NotifyWA = patch.NotifyWA
		list[i].Updated = time.Now().Unix()
		updated := list[i]
		if err := s.save(list); err != nil {
			return nil, err
		}
		return &updated, nil
	}
	return nil, ErrNotFound
}

// Delete removes a TODO. Returns ErrNotFound if id is unknown.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.load()
	if err != nil {
		return err
	}
	for i := range list {
		if list[i].ID == id {
			list = append(list[:i], list[i+1:]...)
			return s.save(list)
		}
	}
	return ErrNotFound
}

// MarkDone flips a TODO to done. For recurring TODOs it bumps Due forward
// by IntervalDays from "now" (not from the previous due — drift would
// accumulate otherwise) and resets Status to pending.
func (s *Store) MarkDone(id string) (*Todo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.load()
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	for i := range list {
		if list[i].ID != id {
			continue
		}
		list[i].LastDone = now
		list[i].DoneCount++
		list[i].Updated = now
		if list[i].IntervalDays > 0 {
			list[i].Status = StatusPending
			list[i].Due = now + int64(list[i].IntervalDays)*86400
			list[i].SnoozeUntil = 0
		} else {
			list[i].Status = StatusDone
		}
		updated := list[i]
		if err := s.save(list); err != nil {
			return nil, err
		}
		return &updated, nil
	}
	return nil, ErrNotFound
}

// Snooze defers a TODO until the given unix timestamp.
func (s *Store) Snooze(id string, until int64) (*Todo, error) {
	if until <= time.Now().Unix() {
		return nil, fmt.Errorf("%w: snooze in the past", ErrBadInput)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.load()
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].ID != id {
			continue
		}
		list[i].Status = StatusSnoozed
		list[i].SnoozeUntil = until
		list[i].Updated = time.Now().Unix()
		updated := list[i]
		if err := s.save(list); err != nil {
			return nil, err
		}
		return &updated, nil
	}
	return nil, ErrNotFound
}

// BucketOf classifies a single TODO. Snoozed TODOs whose SnoozeUntil has
// passed are treated as pending again.
func BucketOf(t Todo, now time.Time) Bucket {
	if t.Status == StatusDone {
		return BucketDone
	}
	if t.Status == StatusSnoozed && t.SnoozeUntil > now.Unix() {
		return BucketFuture
	}
	if t.Due == 0 {
		return BucketFuture
	}
	dt := time.Unix(t.Due, 0).Sub(now)
	switch {
	case dt < 0:
		return BucketOverdue
	case dt < 7*24*time.Hour:
		return BucketWeek
	case dt < 31*24*time.Hour:
		return BucketMonth
	default:
		return BucketFuture
	}
}

// SummaryOf aggregates a list into per-bucket counts.
func SummaryOf(list []Todo, now time.Time) Summary {
	var s Summary
	for _, t := range list {
		switch BucketOf(t, now) {
		case BucketOverdue:
			s.Overdue++
		case BucketWeek:
			s.Week++
		case BucketMonth:
			s.Month++
		case BucketFuture:
			s.Future++
		case BucketDone:
			s.Done++
		}
	}
	return s
}

// newID is the same scheme as audit events: unix nano in base36 → short,
// monotonic, no collisions in practice.
func newID(now int64) string {
	return fmt.Sprintf("t_%s", strconvFormatInt(time.Now().UnixNano(), 36))
}

// strconvFormatInt avoids importing strconv at the top just for one call.
// Inlined hex-style base-N converter.
func strconvFormatInt(n int64, base int) string {
	if n < 0 {
		n = -n
	}
	if n == 0 {
		return "0"
	}
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	var buf [16]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = digits[n%int64(base)]
		n /= int64(base)
	}
	return string(buf[pos:])
}
