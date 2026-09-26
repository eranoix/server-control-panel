// Package scheduler is the cron-driven job runner for vps-manager.
//
// It owns a JSON store of Job definitions (cron expr + kind + args) and a
// single goroutine that ticks every 30s, deciding what to fire and pushing
// each fire as an enqueue into the F3 queue. Run logs, retry, alerting on
// failure, and "run as root" all live here.
//
// Why a separate package rather than expanding queue: the queue is about
// "run this now, capture its output"; the scheduler is about "decide when
// to call enqueue". Keeping them apart means /api/queue still works without
// the scheduler — and tests for either subsystem stay focused.
package scheduler

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

// Job is one scheduled definition.
type Job struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Schedule   string          `json:"schedule"` // standard 5-field cron expr
	Kind       string          `json:"kind"`     // matches a queue.Runner kind
	Args       json.RawMessage `json:"args,omitempty"`
	Enabled    bool            `json:"enabled"`
	Owner      string          `json:"owner"`                 // username; jobs run as this user's identity
	RunAsRoot  bool            `json:"run_as_root,omitempty"` // primary-only; audited
	TimeoutSec int             `json:"timeout_sec,omitempty"` // reserved; runOne currently runs with no timeout (0 = unlimited). Real wiring is a follow-up
	RetryMax   int             `json:"retry_max,omitempty"`   // reserved; no retry today. Real wiring is a follow-up
	AlertOn    AlertMode       `json:"alert_on,omitempty"`
	// Chaining: on completion, fire another task. ThenOn:
	// "success" (default) | "always" | "failure". ThenArgs in the kind's format.
	ThenKind string          `json:"then_kind,omitempty"`
	ThenArgs json.RawMessage `json:"then_args,omitempty"`
	ThenOn   string          `json:"then_on,omitempty"`
	// Notify on completion: sends the result to the chosen channels
	// (notify spine IDs). NotifyOn: "success"|"always"|"failure".
	NotifyChannels []string `json:"notify_channels,omitempty"`
	NotifyOn       string   `json:"notify_on,omitempty"`
	LastFire       int64    `json:"last_fire,omitempty"`
	LastStatus     string   `json:"last_status,omitempty"` // "ok" | "failed" | "skipped"
	LastJobID      string   `json:"last_job_id,omitempty"`
	NextFire       int64    `json:"next_fire,omitempty"` // computed
	Created        int64    `json:"created"`
	Updated        int64    `json:"updated"`
}

// AlertMode controls when failures notify the operator.
//
// VESTIGIAL: routing and silencing of scheduler events now live in the
// notify spine's rules. AlertMode is kept for backwards compatibility — it
// still gates whether the Alerter callback runs at all (and thus whether an
// enqueue failure reaches notify) — but it is no longer where operators
// configure who gets pinged. A scheduled job's execution result flows
// through the queue terminal hook with origin:"scheduler" regardless of this
// knob.
type AlertMode string

const (
	AlertNever  AlertMode = "never"
	AlertFail   AlertMode = "fail" // default — only on failure
	AlertAlways AlertMode = "always"
)

// Enqueuer is the subset of queue.Queue the scheduler uses. Keeps the
// scheduler unit-testable without spinning a real worker pool. Real impl
// is queue.Queue.Enqueue; we wrap it in QueueEnqueuer below.
type Enqueuer interface {
	Enqueue(kind string, args json.RawMessage, owner, source string) (string, error)
}

// Alerter is the failure-notification escape hatch. The HTTP layer wires
// a function that sends a WhatsApp message via the per-user manager.
type Alerter func(owner string, j *Job, jobID string, status string, lastErr string)

// Errors.
var (
	ErrNotFound = errors.New("job not found")
	ErrBadInput = errors.New("invalid input")
)

// Authorizer reports whether owner may run kind. Wired by the HTTP layer to
// Runner.AuthorizedFor. nil = no autonomous-fire gating (tests).
type Authorizer func(owner, kind string) bool

// Scheduler holds the parser + persistence + tick loop.
type Scheduler struct {
	path    string
	parser  cron.Parser
	mu      sync.Mutex
	jobs    map[string]*Job
	enq     Enqueuer
	alert   Alerter
	authz   Authorizer
	stop    chan struct{}
	stopped bool
}

// New constructs a scheduler bound to a JSON file. Caller must call Start
// to fire the tick loop and Stop on shutdown.
func New(path string, enq Enqueuer) (*Scheduler, error) {
	s := &Scheduler{
		path:   path,
		parser: cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow),
		jobs:   map[string]*Job{},
		enq:    enq,
		stop:   make(chan struct{}),
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	s.recomputeAllNextFire()
	return s, nil
}

// SetAlerter wires an Alerter. Optional; nil = no notifications.
func (s *Scheduler) SetAlerter(a Alerter) { s.alert = a }

// OnQueueTerminal is called by the HTTP layer when a queue job reaches a
// terminal state. If the job's source maps to a scheduler job with a chained
// follow-up (ThenKind) and the trigger condition matches, it enqueues the
// follow-up. Chained enqueues use a "scheduler-chain:" source so they never
// chain again (no loops). Authz is rechecked for the follow-up kind.
func (s *Scheduler) OnQueueTerminal(source, status string) {
	id := ""
	switch {
	case strings.HasPrefix(source, "scheduler:"):
		id = strings.TrimPrefix(source, "scheduler:")
	case strings.HasPrefix(source, "scheduler-manual:"):
		id = strings.TrimPrefix(source, "scheduler-manual:")
	default:
		return // "scheduler-chain:" and others do not chain (avoids a loop)
	}
	s.mu.Lock()
	j, ok := s.jobs[id]
	var thenKind, owner, thenOn string
	var thenArgs json.RawMessage
	if ok {
		thenKind, thenArgs, thenOn, owner = j.ThenKind, j.ThenArgs, j.ThenOn, j.Owner
	}
	s.mu.Unlock()
	if !ok || thenKind == "" {
		return
	}
	success := status == "done"
	run := success // default = "success"
	switch thenOn {
	case "always":
		run = true
	case "failure":
		run = !success
	}
	if !run {
		return
	}
	if s.authz != nil && !s.authz(owner, thenKind) {
		log.Printf("scheduler: chain of %s skipped (owner %q lacks permission for kind %q)", id, owner, thenKind)
		return
	}
	if _, err := s.enq.Enqueue(thenKind, thenArgs, owner, "scheduler-chain:"+id); err != nil {
		log.Printf("scheduler: chain of %s failed: %v", id, err)
		return
	}
	log.Printf("scheduler: chain of %s → enqueued %q", id, thenKind)
}

// SetAuthorizer wires the per-fire authorization check. Optional;
// nil = autonomous fires are not gated (the HTTP create/update/run-now gates
// still apply). Guards only the autonomous tick path — manual RunNow is
// already authorized at the HTTP layer.
func (s *Scheduler) SetAuthorizer(a Authorizer) { s.authz = a }

// Start launches the tick goroutine. Idempotent.
func (s *Scheduler) Start() {
	go s.loop()
}

// Stop terminates the tick loop. Idempotent.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return
	}
	close(s.stop)
	s.stopped = true
}

// List returns a snapshot of all jobs sorted by next_fire ascending.
func (s *Scheduler) List(owner string) []*Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		if owner != "" && j.Owner != owner {
			continue
		}
		cp := *j
		out = append(out, &cp)
	}
	sort.SliceStable(out, func(i, k int) bool {
		ai, bi := out[i].NextFire, out[k].NextFire
		if ai == 0 {
			return false
		}
		if bi == 0 {
			return true
		}
		return ai < bi
	})
	return out
}

// Get returns a snapshot of one job by id.
func (s *Scheduler) Get(id string) (*Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *j
	return &cp, nil
}

// Save inserts (when in.ID is empty) or updates an existing job. The cron
// expression is validated; bad expressions are rejected.
func (s *Scheduler) Save(in Job) (*Job, error) {
	if strings.TrimSpace(in.Name) == "" {
		return nil, fmt.Errorf("%w: name", ErrBadInput)
	}
	if _, err := s.parser.Parse(in.Schedule); err != nil {
		return nil, fmt.Errorf("%w: cron expr %q: %v", ErrBadInput, in.Schedule, err)
	}
	if in.Kind == "" {
		return nil, fmt.Errorf("%w: kind", ErrBadInput)
	}
	if in.AlertOn == "" {
		in.AlertOn = AlertFail
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().Unix()
	if in.ID == "" {
		in.ID = fmt.Sprintf("sc_%x", time.Now().UnixNano())
		in.Created = now
	}
	in.Updated = now
	sch, _ := s.parser.Parse(in.Schedule)
	in.NextFire = sch.Next(time.Now()).Unix()
	s.jobs[in.ID] = &in
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	cp := in
	return &cp, nil
}

// Delete removes a job.
func (s *Scheduler) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.jobs[id]; !ok {
		return ErrNotFound
	}
	delete(s.jobs, id)
	return s.saveLocked()
}

// RunNow fires a job immediately as if a tick had hit. Returns the queue
// job ID it enqueued.
func (s *Scheduler) RunNow(id string) (string, error) {
	s.mu.Lock()
	j, ok := s.jobs[id]
	s.mu.Unlock()
	if !ok {
		return "", ErrNotFound
	}
	return s.fire(j, true)
}

// NextFires returns the next n fire timestamps for the given cron expr;
// the UI uses it to preview "next 5 executions" when editing.
func (s *Scheduler) NextFires(expr string, n int) ([]time.Time, error) {
	sch, err := s.parser.Parse(expr)
	if err != nil {
		return nil, err
	}
	if n <= 0 {
		n = 5
	}
	out := make([]time.Time, 0, n)
	t := time.Now()
	for i := 0; i < n; i++ {
		t = sch.Next(t)
		out = append(out, t)
	}
	return out, nil
}

// --- internals ---

func (s *Scheduler) loop() {
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	// fire immediately on startup so jobs that were past-due during a
	// crash still get one tick before the next 30s window.
	s.tickOnce()
	for {
		select {
		case <-s.stop:
			return
		case <-tick.C:
			s.tickOnce()
		}
	}
}

func (s *Scheduler) tickOnce() {
	now := time.Now()
	s.mu.Lock()
	due := make([]*Job, 0)
	for _, j := range s.jobs {
		if !j.Enabled {
			continue
		}
		sch, err := s.parser.Parse(j.Schedule)
		if err != nil {
			continue
		}
		if j.NextFire == 0 {
			j.NextFire = sch.Next(now).Unix()
			continue
		}
		if j.NextFire <= now.Unix() {
			// Catch-up detection: if the scheduled time is more than
			// 5 minutes ago, the host was likely down/paused and we
			// missed at least one firing window. We still fire ONCE
			// (rewinding the timeline would spam the queue with identical jobs)
			// but record the gap so the operator sees it in the audit log.
			missedBy := now.Unix() - j.NextFire
			if missedBy > 300 {
				log.Printf("scheduler: %s late by %ds (host was down/slow?); firing once and continuing",
					j.ID, missedBy)
				j.LastStatus = "late"
			}
			due = append(due, j)
			j.NextFire = sch.Next(now).Unix()
		}
	}
	if len(due) > 0 {
		_ = s.saveLocked()
	}
	s.mu.Unlock()

	for _, j := range due {
		if _, err := s.fire(j, false); err != nil {
			log.Printf("scheduler: fire %s: %v", j.ID, err)
		}
	}
}

// fire enqueues a single job. The "manual" flag flips audit trail / source.
//
// fire is the single chokepoint for POST/PUT/run-now (manual=true) and the
// autonomous tick (manual=false). The autonomous path is gated here by the
// Authorizer: a job whose owner is no longer allowed to run its kind
// (e.g. saved before the authz fix, or whose kind became primary-only) is
// skipped instead of enqueued — closing the bypass at the tick. We return
// (nil) rather than an error so tickOnce doesn't log "fire: ..." every cron
// interval (a `* * * * *` orphan would otherwise spam the log); LastStatus is
// recorded as "skipped" and a single informative line is logged. Manual fires
// are already authorized at the HTTP layer, so !manual scopes the gate to the
// autonomous path only.
func (s *Scheduler) fire(j *Job, manual bool) (string, error) {
	if !manual && s.authz != nil && !s.authz(j.Owner, j.Kind) {
		s.mu.Lock()
		j.LastFire = time.Now().Unix()
		j.LastStatus = "skipped"
		_ = s.saveLocked()
		s.mu.Unlock()
		log.Printf("scheduler: %s skipped (owner %q lacks permission for kind %q)", j.ID, j.Owner, j.Kind)
		return "", nil
	}
	source := "scheduler:" + j.ID
	if manual {
		source = "scheduler-manual:" + j.ID
	}
	qid, err := s.enq.Enqueue(j.Kind, j.Args, j.Owner, source)
	if err != nil {
		s.mu.Lock()
		j.LastFire = time.Now().Unix()
		j.LastStatus = "failed"
		s.saveLocked()
		s.mu.Unlock()
		if s.alert != nil && (j.AlertOn == AlertFail || j.AlertOn == AlertAlways) {
			s.alert(j.Owner, j, "", "failed", err.Error())
		}
		return "", err
	}
	s.mu.Lock()
	j.LastFire = time.Now().Unix()
	j.LastStatus = "ok"
	j.LastJobID = qid
	_ = s.saveLocked()
	s.mu.Unlock()
	if s.alert != nil && j.AlertOn == AlertAlways {
		s.alert(j.Owner, j, qid, "ok", "")
	}
	return qid, nil
}

func (s *Scheduler) recomputeAllNextFire() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for _, j := range s.jobs {
		if !j.Enabled {
			j.NextFire = 0
			continue
		}
		sch, err := s.parser.Parse(j.Schedule)
		if err != nil {
			continue
		}
		j.NextFire = sch.Next(now).Unix()
	}
	_ = s.saveLocked()
}

func (s *Scheduler) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(data) == 0 {
		return nil
	}
	var list []*Job
	if err := json.Unmarshal(data, &list); err != nil {
		return fmt.Errorf("scheduler jobs corrupt: %w", err)
	}
	for _, j := range list {
		s.jobs[j.ID] = j
	}
	return nil
}

func (s *Scheduler) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	list := make([]*Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		list = append(list, j)
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	// fsync before the rename — without it the rename updates the inode but
	// the bytes may not have reached the disk; a power loss just after the
	// rename can bring scheduler.json back empty or truncated.
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
	// Atomic write order: tmp+rename. The previous sequence (rename
	// path→bak, then rename tmp→path) left a window where a crash
	// between renames meant NO scheduler.json existed — boot would
	// start with zero jobs. New order: rename tmp→path (single atomic
	// op POSIX-guaranteed) THEN copy the new file to .bak as a separate
	// best-effort backup. If the .bak copy fails, scheduler.json is
	// still correct and the next save retries the backup.
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	// Best-effort backup; never let backup failure poison the save.
	if data2, err := os.ReadFile(s.path); err == nil {
		_ = os.WriteFile(s.path+".bak", data2, 0o600)
	}
	return nil
}
