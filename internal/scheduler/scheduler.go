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

type Job struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	Schedule       string          `json:"schedule"`
	Kind           string          `json:"kind"`
	Args           json.RawMessage `json:"args,omitempty"`
	Enabled        bool            `json:"enabled"`
	Owner          string          `json:"owner"`
	RunAsRoot      bool            `json:"run_as_root,omitempty"`
	TimeoutSec     int             `json:"timeout_sec,omitempty"`
	RetryMax       int             `json:"retry_max,omitempty"`
	AlertOn        AlertMode       `json:"alert_on,omitempty"`
	ThenKind       string          `json:"then_kind,omitempty"`
	ThenArgs       json.RawMessage `json:"then_args,omitempty"`
	ThenOn         string          `json:"then_on,omitempty"`
	NotifyChannels []string        `json:"notify_channels,omitempty"`
	NotifyOn       string          `json:"notify_on,omitempty"`
	LastFire       int64           `json:"last_fire,omitempty"`
	LastStatus     string          `json:"last_status,omitempty"`
	LastJobID      string          `json:"last_job_id,omitempty"`
	NextFire       int64           `json:"next_fire,omitempty"`
	Created        int64           `json:"created"`
	Updated        int64           `json:"updated"`
}

type AlertMode string

const (
	AlertNever  AlertMode = "never"
	AlertFail   AlertMode = "fail"
	AlertAlways AlertMode = "always"
)

type Enqueuer interface {
	Enqueue(kind string, args json.RawMessage, owner, source string) (string, error)
}

type Alerter func(owner string, j *Job, jobID string, status string, lastErr string)

var (
	ErrNotFound = errors.New("job not found")
	ErrBadInput = errors.New("invalid input")
)

type Authorizer func(owner, kind string) bool

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

func (s *Scheduler) SetAlerter(a Alerter) { s.alert = a }

func (s *Scheduler) OnQueueTerminal(source, status string) {
	id := ""
	switch {
	case strings.HasPrefix(source, "scheduler:"):
		id = strings.TrimPrefix(source, "scheduler:")
	case strings.HasPrefix(source, "scheduler-manual:"):
		id = strings.TrimPrefix(source, "scheduler-manual:")
	default:
		return
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
	run := success
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

func (s *Scheduler) SetAuthorizer(a Authorizer) { s.authz = a }

func (s *Scheduler) Start() {
	go s.loop()
}

func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return
	}
	close(s.stop)
	s.stopped = true
}

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

func (s *Scheduler) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.jobs[id]; !ok {
		return ErrNotFound
	}
	delete(s.jobs, id)
	return s.saveLocked()
}

func (s *Scheduler) RunNow(id string) (string, error) {
	s.mu.Lock()
	j, ok := s.jobs[id]
	s.mu.Unlock()
	if !ok {
		return "", ErrNotFound
	}
	return s.fire(j, true)
}

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

func (s *Scheduler) loop() {
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
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
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	if data2, err := os.ReadFile(s.path); err == nil {
		_ = os.WriteFile(s.path+".bak", data2, 0o600)
	}
	return nil
}
