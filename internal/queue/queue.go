package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type Status string

const (
	StatusQueued      Status = "queued"
	StatusRunning     Status = "running"
	StatusDone        Status = "done"
	StatusFailed      Status = "failed"
	StatusCancelled   Status = "cancelled"
	StatusInterrupted Status = "interrupted"
)

const detachStartupGrace = 15

type Job struct {
	ID               string          `json:"id"`
	Kind             string          `json:"kind"`
	Args             json.RawMessage `json:"args,omitempty"`
	Owner            string          `json:"owner"`
	Status           Status          `json:"status"`
	Progress         int             `json:"progress"`
	Step             string          `json:"step,omitempty"`
	LogPath          string          `json:"log_path,omitempty"`
	Error            string          `json:"error,omitempty"`
	Queued           int64           `json:"queued"`
	Started          int64           `json:"started,omitempty"`
	Finished         int64           `json:"finished,omitempty"`
	Source           string          `json:"source,omitempty"`
	Scope            string          `json:"scope,omitempty"`
	cancel           context.CancelFunc
	notifiedTerminal bool
}

type Runner interface {
	Kind() string
	Run(ctx context.Context, args json.RawMessage, logW io.Writer, progress func(int), step func(string)) error
	AuthorizedFor(user string, isPrimary bool) bool
}

type Queue struct {
	dataDir        string
	workers        int
	mu             sync.Mutex
	jobs           map[string]*Job
	order          []string
	maxKeep        int
	pending        chan string
	runners        map[string]Runner
	listeners      map[string][]chan Event
	listMu         sync.Mutex
	shutdown       chan struct{}
	wg             sync.WaitGroup
	detachKinds    map[string]bool
	detachLauncher func(id string) (string, error)
	notifier       func(*Job)
}

type DetachedStatus struct {
	Status   Status `json:"status"`
	Progress int    `json:"progress"`
	Step     string `json:"step,omitempty"`
	Error    string `json:"error,omitempty"`
	Started  int64  `json:"started,omitempty"`
	Finished int64  `json:"finished,omitempty"`
}

func isTerminal(s Status) bool {
	return s == StatusDone || s == StatusFailed || s == StatusCancelled || s == StatusInterrupted
}

func detachedDir(queueDir string) string { return filepath.Join(queueDir, "detached") }
func detachedPath(queueDir, id string) string {
	return filepath.Join(detachedDir(queueDir), id+".json")
}

func WriteDetachedStatus(queueDir, id string, s DetachedStatus) error {
	if err := os.MkdirAll(detachedDir(queueDir), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	path := detachedPath(queueDir, id)
	tmp := path + ".tmp"
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
	return os.Rename(tmp, path)
}

func readDetachedStatus(queueDir, id string) (DetachedStatus, bool) {
	data, err := os.ReadFile(detachedPath(queueDir, id))
	if err != nil {
		return DetachedStatus{}, false
	}
	var s DetachedStatus
	if err := json.Unmarshal(data, &s); err != nil {
		return DetachedStatus{}, false
	}
	return s, true
}

type Event struct {
	Type     string `json:"type"`
	JobID    string `json:"job_id"`
	Status   Status `json:"status,omitempty"`
	Progress int    `json:"progress,omitempty"`
	Step     string `json:"step,omitempty"`
	LogLine  string `json:"log,omitempty"`
	TS       int64  `json:"ts"`
}

type Options struct {
	DataDir string
	Workers int
	MaxKeep int
}

var (
	ErrUnknownKind = errors.New("unknown job kind")
	ErrForbidden   = errors.New("not authorised for this job kind")
	ErrNotFound    = errors.New("job not found")
	ErrConflict    = errors.New("job not deletable while active")
)

func SafeToResume(kind string) bool {
	switch kind {
	case "apt_upgrade", "docker_pull", "docker_compose_pull", "image_prune", "backup_now":
		return true
	default:
		return false
	}
}

func scopeAlive(scope string) bool {
	if scope == "" {
		return false
	}
	return exec.Command("systemctl", "is-active", "--quiet", scope).Run() == nil
}

func scopeStop(scope string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "systemctl", "stop", scope).Run()
}

func (q *Queue) pushPending(id string) {
	select {
	case q.pending <- id:
	default:
		go func() { q.pending <- id }()
	}
}

func NewQueue(opts Options) (*Queue, error) {
	if opts.Workers <= 0 {
		opts.Workers = 3
	}
	if opts.MaxKeep <= 0 {
		opts.MaxKeep = 500
	}
	root := filepath.Join(opts.DataDir, "queue")
	if err := os.MkdirAll(filepath.Join(root, "runs"), 0o700); err != nil {
		return nil, err
	}
	q := &Queue{
		dataDir:   root,
		workers:   opts.Workers,
		maxKeep:   opts.MaxKeep,
		jobs:      make(map[string]*Job),
		runners:   make(map[string]Runner),
		listeners: make(map[string][]chan Event),
		pending:   make(chan string, 256),
		shutdown:  make(chan struct{}),
	}
	if err := q.load(); err != nil {
		return nil, err
	}
	for _, j := range q.jobs {
		switch j.Status {
		case StatusQueued:
			q.pushPending(j.ID)
		case StatusRunning:
			if j.Scope != "" {
				if scopeAlive(j.Scope) {
					continue
				}
				if ds, ok := readDetachedStatus(q.dataDir, j.ID); ok && isTerminal(ds.Status) {
					applyDetached(j, ds)
					continue
				}
				j.Status = StatusInterrupted
				j.Error = "interrupted: server-control-panel restart"
				j.Finished = time.Now().Unix()
				continue
			}
			if SafeToResume(j.Kind) {
				j.Status = StatusQueued
				j.Started, j.Progress = 0, 0
				q.pushPending(j.ID)
			} else {
				j.Status = StatusInterrupted
				j.Error = "interrupted: server-control-panel restart"
				j.Finished = time.Now().Unix()
			}
		}
	}
	q.persist()
	for i := 0; i < q.workers; i++ {
		q.wg.Add(1)
		go q.workerLoop()
	}
	go q.detachReaper()
	return q, nil
}

func applyDetached(j *Job, ds DetachedStatus) {
	if ds.Status != "" {
		j.Status = ds.Status
	}
	if ds.Progress > j.Progress {
		j.Progress = ds.Progress
	}
	if ds.Step != "" {
		j.Step = ds.Step
	}
	if ds.Error != "" {
		j.Error = ds.Error
	}
	if ds.Started != 0 {
		j.Started = ds.Started
	}
	if isTerminal(ds.Status) {
		if ds.Finished != 0 {
			j.Finished = ds.Finished
		} else if j.Finished == 0 {
			j.Finished = time.Now().Unix()
		}
		if ds.Status == StatusDone && j.Progress < 100 {
			j.Progress = 100
		}
	}
}

func (q *Queue) detachReaper() {
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-q.shutdown:
			return
		case <-t.C:
			q.reapDetachedOnce()
		}
	}
}

func (q *Queue) reapDetachedOnce() {
	q.mu.Lock()
	var scoped []*Job
	for _, j := range q.jobs {
		if j.Scope != "" && j.Status == StatusRunning {
			scoped = append(scoped, j)
		}
	}
	q.mu.Unlock()
	if len(scoped) == 0 {
		return
	}
	for _, j := range scoped {
		ds, ok := readDetachedStatus(q.dataDir, j.ID)
		if ok && !isTerminal(ds.Status) &&
			time.Now().Unix()-j.Started >= detachStartupGrace && !scopeAlive(j.Scope) {
			ok = false
		}
		q.mu.Lock()
		changed := false
		if ok {
			before := *j
			applyDetached(j, ds)
			changed = before.Status != j.Status || before.Progress != j.Progress || before.Step != j.Step
		} else if time.Now().Unix()-j.Started >= detachStartupGrace && !scopeAlive(j.Scope) {
			j.Status = StatusInterrupted
			j.Error = "interrupted: detached process vanished"
			j.Finished = time.Now().Unix()
			changed = true
		}
		terminal := isTerminal(j.Status)
		if changed {
			q.persistLocked()
		}
		if terminal && changed {
			q.notifyTerminalLocked(j)
		}
		st := j.Status
		prog := j.Progress
		step := j.Step
		q.mu.Unlock()
		if changed {
			q.broadcast(j.ID, Event{Type: "progress", JobID: j.ID, Progress: prog, TS: time.Now().Unix()})
			if step != "" {
				q.broadcast(j.ID, Event{Type: "step", JobID: j.ID, Step: step, TS: time.Now().Unix()})
			}
			if terminal {
				q.broadcast(j.ID, Event{Type: "status", JobID: j.ID, Status: st, TS: time.Now().Unix()})
			}
		}
	}
}

func (q *Queue) Register(r Runner) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.runners[r.Kind()] = r
}

func (q *Queue) SetDetach(launcher func(id string) (string, error), kinds ...string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.detachLauncher = launcher
	q.detachKinds = make(map[string]bool, len(kinds))
	for _, k := range kinds {
		q.detachKinds[k] = true
	}
}

func (q *Queue) SetNotifier(fn func(*Job)) {
	q.mu.Lock()
	q.notifier = fn
	q.mu.Unlock()
}

func (q *Queue) notifyTerminalLocked(j *Job) {
	if q.notifier == nil {
		return
	}
	if !isTerminal(j.Status) || j.notifiedTerminal {
		return
	}
	j.notifiedTerminal = true
	jc := *j
	q.notifier(&jc)
}

func (q *Queue) Enqueue(kind string, args json.RawMessage, owner, source string) (*Job, error) {
	q.mu.Lock()
	if _, ok := q.runners[kind]; !ok {
		q.mu.Unlock()
		return nil, ErrUnknownKind
	}
	q.mu.Unlock()

	now := time.Now()
	id := fmt.Sprintf("j_%x", now.UnixNano())
	logPath := filepath.Join(q.dataDir, "runs", id+".log")
	j := &Job{
		ID:      id,
		Kind:    kind,
		Args:    args,
		Owner:   owner,
		Status:  StatusQueued,
		Queued:  now.Unix(),
		LogPath: logPath,
		Source:  source,
	}
	q.mu.Lock()
	q.jobs[id] = j
	q.order = append(q.order, id)
	q.pruneLocked()
	q.persistLocked()
	q.mu.Unlock()

	q.dispatch(j)
	cp := q.snapshot(id)
	if cp == nil {
		cp = j
	}
	return cp, nil
}

func (q *Queue) dispatch(j *Job) {
	q.mu.Lock()
	detach := q.detachKinds[j.Kind] && q.detachLauncher != nil
	launcher := q.detachLauncher
	q.mu.Unlock()

	if detach {
		_ = os.Remove(detachedPath(q.dataDir, j.ID))
		if scope, err := launcher(j.ID); err == nil && scope != "" {
			q.mu.Lock()
			j.Scope = scope
			j.Status = StatusRunning
			j.Started = time.Now().Unix()
			q.persistLocked()
			ts := j.Started
			q.mu.Unlock()
			q.broadcast(j.ID, Event{Type: "status", JobID: j.ID, Status: StatusRunning, TS: ts})
			return
		}
		q.mu.Lock()
		j.Scope = ""
		q.persistLocked()
		q.mu.Unlock()
	}
	q.pushPending(j.ID)
}

func (q *Queue) snapshot(id string) *Job {
	q.mu.Lock()
	defer q.mu.Unlock()
	j, ok := q.jobs[id]
	if !ok {
		return nil
	}
	cp := *j
	cp.cancel = nil
	return &cp
}

func (q *Queue) Get(id string) (*Job, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	j, ok := q.jobs[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *j
	cp.cancel = nil
	return &cp, nil
}

func (q *Queue) List(owner string, status Status, limit int) []*Job {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]*Job, 0, len(q.jobs))
	for _, id := range q.order {
		j := q.jobs[id]
		if j == nil {
			continue
		}
		if owner != "" && j.Owner != owner {
			continue
		}
		if status != "" && j.Status != status {
			continue
		}
		cp := *j
		cp.cancel = nil
		out = append(out, &cp)
	}
	sort.SliceStable(out, func(i, k int) bool { return out[i].Queued > out[k].Queued })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (q *Queue) ListBySource(sources map[string]bool, limit int) []*Job {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]*Job, 0)
	for _, id := range q.order {
		j := q.jobs[id]
		if j == nil || !sources[j.Source] {
			continue
		}
		cp := *j
		cp.cancel = nil
		out = append(out, &cp)
	}
	sort.SliceStable(out, func(i, k int) bool { return out[i].Queued > out[k].Queued })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (q *Queue) Cancel(id string) error {
	q.mu.Lock()
	j, ok := q.jobs[id]
	if !ok {
		q.mu.Unlock()
		return ErrNotFound
	}
	if j.Status == StatusDone || j.Status == StatusFailed || j.Status == StatusCancelled || j.Status == StatusInterrupted {
		q.mu.Unlock()
		return nil
	}
	cancel := j.cancel
	scope := ""
	if j.Scope != "" && j.Status == StatusRunning {
		scope = j.Scope
	}
	if j.Status == StatusQueued || scope != "" {
		j.Status = StatusCancelled
		j.Finished = time.Now().Unix()
		q.persistLocked()
		q.notifyTerminalLocked(j)
	}
	q.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if scope != "" {
		_ = scopeStop(scope)
	}
	q.broadcast(id, Event{Type: "status", JobID: id, Status: StatusCancelled, TS: time.Now().Unix()})
	return nil
}

func (q *Queue) Delete(id string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	j, ok := q.jobs[id]
	if !ok {
		return ErrNotFound
	}
	if j.Status == StatusQueued || j.Status == StatusRunning {
		return ErrConflict
	}
	delete(q.jobs, id)
	out := q.order[:0]
	for _, oid := range q.order {
		if oid != id {
			out = append(out, oid)
		}
	}
	q.order = out
	if j.LogPath != "" {
		if err := os.Remove(j.LogPath); err != nil && !os.IsNotExist(err) {
		}
	}
	_ = os.Remove(detachedPath(q.dataDir, id))
	q.persistLocked()
	return nil
}

func (q *Queue) Rerun(id string) (*Job, error) {
	q.mu.Lock()
	j, ok := q.jobs[id]
	if !ok {
		q.mu.Unlock()
		return nil, ErrNotFound
	}
	if j.Status == StatusQueued || j.Status == StatusRunning {
		q.mu.Unlock()
		return nil, ErrConflict
	}
	if _, ok := q.runners[j.Kind]; !ok {
		q.mu.Unlock()
		return nil, ErrUnknownKind
	}
	j.Status = StatusQueued
	j.Progress = 0
	j.Error = ""
	j.Started = 0
	j.Finished = 0
	j.Step = ""
	j.Scope = ""
	j.Queued = time.Now().Unix()
	j.cancel = nil
	queuedTS := j.Queued
	q.persistLocked()
	q.mu.Unlock()

	q.broadcast(id, Event{Type: "status", JobID: id, Status: StatusQueued, TS: queuedTS})
	q.dispatch(j)
	cp := q.snapshot(id)
	if cp == nil {
		cp = j
	}
	return cp, nil
}

func (q *Queue) Subscribe(jobID string) (<-chan Event, func()) {
	ch := make(chan Event, 64)
	q.listMu.Lock()
	q.listeners[jobID] = append(q.listeners[jobID], ch)
	q.listMu.Unlock()
	return ch, func() {
		q.listMu.Lock()
		defer q.listMu.Unlock()
		list := q.listeners[jobID]
		for i, c := range list {
			if c == ch {
				q.listeners[jobID] = append(list[:i], list[i+1:]...)
				break
			}
		}
		close(ch)
	}
}

func (q *Queue) ReadLog(id string, maxBytes int64) ([]byte, error) {
	q.mu.Lock()
	j, ok := q.jobs[id]
	q.mu.Unlock()
	if !ok {
		return nil, ErrNotFound
	}
	if j.LogPath == "" {
		return nil, nil
	}
	f, err := os.Open(j.LogPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	if maxBytes > 0 {
		info, _ := f.Stat()
		if info != nil && info.Size() > maxBytes {
			_, _ = f.Seek(info.Size()-maxBytes, io.SeekStart)
		}
	}
	return io.ReadAll(f)
}

func (q *Queue) Shutdown(ctx context.Context) {
	close(q.shutdown)
	q.mu.Lock()
	for _, j := range q.jobs {
		if j.cancel != nil {
			j.cancel()
		}
	}
	q.mu.Unlock()

	deadline := 4 * time.Second
	if dl, ok := ctx.Deadline(); ok {
		if r := time.Until(dl); r < deadline {
			deadline = r
		}
	}
	if deadline < 0 {
		deadline = 0
	}

	done := make(chan struct{})
	go func() { q.wg.Wait(); close(done) }()
	select {
	case <-done:
		q.mu.Lock()
		q.persistLocked()
		q.mu.Unlock()
	case <-time.After(deadline):
		q.mu.Lock()
		for _, j := range q.jobs {
			if j.Status == StatusRunning && j.Scope == "" {
				j.Status = StatusInterrupted
				j.Error = "interrupted: shutdown timeout"
				j.Finished = time.Now().Unix()
			}
		}
		q.persistLocked()
		q.mu.Unlock()
	}
}

func (q *Queue) Counts() (running, queued int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, j := range q.jobs {
		switch j.Status {
		case StatusRunning:
			if j.Scope == "" {
				running++
			}
		case StatusQueued:
			queued++
		}
	}
	return running, queued
}

func (q *Queue) workerLoop() {
	defer q.wg.Done()
	for {
		select {
		case <-q.shutdown:
			return
		case id := <-q.pending:
			q.runOne(id)
		}
	}
}

func (q *Queue) runOne(id string) {
	q.mu.Lock()
	j, ok := q.jobs[id]
	if !ok {
		q.mu.Unlock()
		return
	}
	if j.Status == StatusCancelled {
		q.mu.Unlock()
		return
	}
	runner, ok := q.runners[j.Kind]
	if !ok {
		j.Status = StatusFailed
		j.Error = "unknown kind: " + j.Kind
		j.Finished = time.Now().Unix()
		q.persistLocked()
		q.notifyTerminalLocked(j)
		q.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	j.cancel = cancel
	j.Status = StatusRunning
	j.Started = time.Now().Unix()
	q.persistLocked()
	q.mu.Unlock()
	q.broadcast(id, Event{Type: "status", JobID: id, Status: StatusRunning, TS: j.Started})

	logF, err := os.Create(j.LogPath)
	if err != nil {
		q.fail(id, "create log: "+err.Error())
		return
	}
	defer logF.Close()

	bw := &broadcastWriter{q: q, jobID: id}
	mw := io.MultiWriter(logF, bw)

	prog := func(p int) {
		if p < 0 {
			p = 0
		}
		if p > 100 {
			p = 100
		}
		q.mu.Lock()
		j.Progress = p
		q.persistLocked()
		q.mu.Unlock()
		q.broadcast(id, Event{Type: "progress", JobID: id, Progress: p, TS: time.Now().Unix()})
	}

	setStep := func(s string) {
		q.mu.Lock()
		j.Step = s
		q.persistLocked()
		q.mu.Unlock()
		q.broadcast(id, Event{Type: "step", JobID: id, Step: s, TS: time.Now().Unix()})
	}

	err = runner.Run(ctx, j.Args, mw, prog, setStep)
	q.mu.Lock()
	defer q.mu.Unlock()
	j.cancel = nil
	j.Finished = time.Now().Unix()
	switch {
	case ctx.Err() == context.Canceled:
		j.Status = StatusCancelled
	case err != nil:
		j.Status = StatusFailed
		j.Error = err.Error()
		fmt.Fprintf(logF, "\n--- FAILED: %v ---\n", err)
	default:
		j.Status = StatusDone
		j.Progress = 100
	}
	q.persistLocked()
	q.broadcast(id, Event{Type: "status", JobID: id, Status: j.Status, TS: j.Finished})
	q.notifyTerminalLocked(j)
}

func (q *Queue) fail(id, msg string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	j, ok := q.jobs[id]
	if !ok {
		return
	}
	j.Status = StatusFailed
	j.Error = msg
	j.Finished = time.Now().Unix()
	q.persistLocked()
	q.broadcast(id, Event{Type: "status", JobID: id, Status: StatusFailed, TS: j.Finished})
	q.notifyTerminalLocked(j)
}

func (q *Queue) broadcast(jobID string, ev Event) {
	q.listMu.Lock()
	defer q.listMu.Unlock()
	for _, ch := range q.listeners[jobID] {
		select {
		case ch <- ev:
		default:
		}
	}
}

func (q *Queue) pruneLocked() {
	if len(q.order) <= q.maxKeep {
		return
	}
	keep := q.order[:0]
	dropped := 0
	for _, id := range q.order {
		j := q.jobs[id]
		if j != nil {
			finished := j.Status == StatusDone || j.Status == StatusFailed || j.Status == StatusCancelled || j.Status == StatusInterrupted
			if finished && len(q.order)-dropped > q.maxKeep {
				delete(q.jobs, id)
				_ = os.Remove(j.LogPath)
				_ = os.Remove(detachedPath(q.dataDir, id))
				dropped++
				continue
			}
		}
		keep = append(keep, id)
	}
	q.order = keep
}

func (q *Queue) load() error {
	data, err := os.ReadFile(filepath.Join(q.dataDir, "state.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var stored struct {
		Jobs  []*Job   `json:"jobs"`
		Order []string `json:"order"`
	}
	if len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		return fmt.Errorf("queue state corrupt: %w", err)
	}
	for _, j := range stored.Jobs {
		q.jobs[j.ID] = j
	}
	q.order = stored.Order
	return nil
}

func (q *Queue) persist() {
	q.mu.Lock()
	q.persistLocked()
	q.mu.Unlock()
}

func (q *Queue) persistLocked() {
	jobs := make([]*Job, 0, len(q.jobs))
	for _, j := range q.jobs {
		jobs = append(jobs, j)
	}
	stored := struct {
		Jobs  []*Job   `json:"jobs"`
		Order []string `json:"order"`
	}{Jobs: jobs, Order: q.order}
	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return
	}
	path := filepath.Join(q.dataDir, "state.json")
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return
	}
	_ = os.Rename(tmp, path)
}

type broadcastWriter struct {
	q     *Queue
	jobID string
}

func (b *broadcastWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	b.q.broadcast(b.jobID, Event{Type: "log", JobID: b.jobID, LogLine: string(p), TS: time.Now().Unix()})
	return len(p), nil
}
