// Package queue is the asynchronous background-job runner for vps-manager.
//
// Operations that used to block the HTTP request (apt-upgrade, docker pull,
// image prune, ZFS snapshot, arbitrary shell) are submitted here instead.
// A small worker pool picks them up, captures combined stdout+stderr to
// per-job log files, persists state to <DataDir>/queue/state.json, and lets
// a WS handler tail the log live.
//
// The Scheduler (F2) reuses this queue: a cron tick that needs to run a job
// just calls queue.Enqueue with the appropriate kind. That keeps the runner
// pool, retry semantics and observability in one place.
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

// Status is the lifecycle of a single job execution.
type Status string

const (
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusDone      Status = "done"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
	// StatusInterrupted is an HONEST terminal state for a job that was
	// running when the process died (deploy/restart) and could NOT be safely
	// resumed (see SafeToResume). It is distinct from StatusFailed: nothing
	// the job *did* failed — the host went away mid-flight. It is terminal,
	// so it flows through the same Delete/Rerun guards as failed/cancelled
	// (both block only queued||running), making it deletable and re-runnable
	// for free. Replaces the old "failed: interrupted" the user saw as red.
	StatusInterrupted Status = "interrupted"
)

// detachStartupGrace is how long (seconds) the reaper waits before treating a
// detached job with no status file as crashed. Covers the window between
// launch and the external process's first WriteDetachedStatus while
// systemd-run spins the scope up.
const detachStartupGrace = 15

// Job is the serialised job record. Args are runner-specific; treat as
// opaque JSON outside the runner.
type Job struct {
	ID       string          `json:"id"`
	Kind     string          `json:"kind"`
	Args     json.RawMessage `json:"args,omitempty"`
	Owner    string          `json:"owner"`
	Status   Status          `json:"status"`
	Progress int             `json:"progress"`
	// Step is a short human-readable label for the current phase ("clonando
	// repo", "auditando"). Optional: runners that only track Progress leave
	// it empty. Lets the UI show "where are we" without opening the log.
	Step     string `json:"step,omitempty"`
	LogPath  string `json:"log_path,omitempty"`
	Error    string `json:"error,omitempty"`
	Queued   int64  `json:"queued"`
	Started  int64  `json:"started,omitempty"`
	Finished int64  `json:"finished,omitempty"`
	Source   string `json:"source,omitempty"` // "user" | "scheduler:<job_id>"
	// Scope is the systemd transient scope unit name when this job runs
	// DETACHED (jira_ai_analysis is launched via `systemd-run --scope`).
	// Empty for in-process jobs. When set and the scope is still alive at
	// boot, the reconcile leaves the job's status to the detached owner
	// instead of marking it interrupted. Inert until the detach path
	// populates it.
	Scope  string `json:"scope,omitempty"`
	cancel context.CancelFunc
	// notifiedTerminal is the set-once guard: true after the terminal
	// notification hook has fired for this job. Unexported → NOT persisted to
	// state.json (json never touches unexported fields). Losing it on restart
	// is harmless by design: boot reconcile never calls the hook, and a job
	// already terminal in state.json is never re-written, so it never
	// re-notifies. See notifyTerminalLocked.
	notifiedTerminal bool
}

// Runner is the contract every job kind implements.
//   - ctx is cancelled when Cancel is called on the queue
//   - args is the per-job payload (opaque outside the runner)
//   - logW receives combined stdout+stderr; runner controls framing
//   - progress callback is optional but recommended for long jobs
//   - step callback is optional; emits a short phase label ("auditando")
//     surfaced inline in the UI so users see progress without the log
type Runner interface {
	Kind() string
	Run(ctx context.Context, args json.RawMessage, logW io.Writer, progress func(int), step func(string)) error
	// AuthorizedFor returns true if user is allowed to enqueue this kind.
	// Defaults (when not implemented via wrapper) should be "any
	// authenticated user". Returning false yields HTTP 403.
	AuthorizedFor(user string, isPrimary bool) bool
}

// Queue is the worker pool + state store.
type Queue struct {
	dataDir   string // queue persistence root (e.g. <DataDir>/queue)
	workers   int
	mu        sync.Mutex
	jobs      map[string]*Job
	order     []string    // append-only id history; trimmed at maxKeep
	maxKeep   int         // history cap; oldest finished pruned
	pending   chan string // ids waiting for a worker
	runners   map[string]Runner
	listeners map[string][]chan Event // jobID → live tailers (WS)
	listMu    sync.Mutex
	shutdown  chan struct{}
	// wg tracks the worker goroutines so Shutdown can wait for them to
	// finish before the final persist. Without it, Shutdown signals but may
	// return before the in-flight worker updates j.Status/Finished
	// — the restart then re-runs it as interrupted, or loses the update.
	wg sync.WaitGroup
	// detachKinds + detachLauncher: kinds in detachKinds run in a
	// DETACHED systemd scope instead of the in-process worker pool, so a
	// deploy/restart of vps-manager doesn't kill them. detachLauncher starts
	// the external process and returns its scope unit name. Both nil/empty
	// when detach is disabled → everything runs in-process as before.
	detachKinds    map[string]bool
	detachLauncher func(id string) (string, error)
	// notifier is the terminal-job hook (notify spine). nil until
	// SetNotifier wires it AFTER boot — so the reconcile inside NewQueue, which
	// runs with notifier still nil, never fires. Called ONLY under q.mu, via
	// notifyTerminalLocked, from the 5 runtime terminal sites. Decoupled by
	// design: queue knows nothing of notify — the wiring closure maps Job→Event.
	notifier func(*Job)
}

// DetachedStatus is the per-job status file a DETACHED run-job process owns.
// STRICT WRITER OWNERSHIP is the whole point: the detached process is the
// SOLE writer of <queueDir>/detached/<id>.json, and the main process ONLY
// reads it — it never writes that file, and the detached process never writes
// state.json. That clean split is how we avoid a last-writer-wins race on the
// shared state.json between two live processes (the residual risk the detach
// path had to close). The main process merges this into its in-memory view
// via the reaper.
type DetachedStatus struct {
	Status   Status `json:"status"`
	Progress int    `json:"progress"`
	Step     string `json:"step,omitempty"`
	Error    string `json:"error,omitempty"`
	Started  int64  `json:"started,omitempty"`
	Finished int64  `json:"finished,omitempty"`
}

// isTerminal reports whether a status is a final resting state.
func isTerminal(s Status) bool {
	return s == StatusDone || s == StatusFailed || s == StatusCancelled || s == StatusInterrupted
}

func detachedDir(queueDir string) string { return filepath.Join(queueDir, "detached") }
func detachedPath(queueDir, id string) string {
	return filepath.Join(detachedDir(queueDir), id+".json")
}

// WriteDetachedStatus atomically writes the per-job status file. Called ONLY
// by the detached run-job process (never by the main process). queueDir is
// the queue root, i.e. <DataDir>/queue.
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

// readDetachedStatus reads the per-job status file. Returns ok=false when the
// file is absent or unreadable (the detached process hasn't written yet, or
// there is no detached process). Called ONLY by the main process.
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

// Event is what live listeners receive on the WS endpoint.
type Event struct {
	Type     string `json:"type"` // "status" | "progress" | "step" | "log"
	JobID    string `json:"job_id"`
	Status   Status `json:"status,omitempty"`
	Progress int    `json:"progress,omitempty"`
	Step     string `json:"step,omitempty"`
	LogLine  string `json:"log,omitempty"`
	TS       int64  `json:"ts"`
}

// Options configures NewQueue.
type Options struct {
	DataDir string // base data dir; queue stores under <DataDir>/queue
	Workers int    // worker count; defaults to 3
	MaxKeep int    // max persisted jobs (oldest finished pruned); default 500
}

var (
	ErrUnknownKind = errors.New("unknown job kind")
	ErrForbidden   = errors.New("not authorised for this job kind")
	ErrNotFound    = errors.New("job not found")
	ErrConflict    = errors.New("job not deletable while active")
)

// SafeToResume reports whether a job of this kind can be automatically
// re-queued after an interrupted run WITHOUT side effects (duplicate
// comments, double charges, etc). Idempotent maintenance jobs are safe; a
// job whose runner APPENDS state on each run is not.
//
// jira_ai_analysis is deliberately NOT safe: its UpdateIssue/mergeAIBlock/
// stripAIBlock/mergeLabels are idempotent (they REPLACE the AI block), but
// AddComment (runner.go) APPENDS a comment with no dedup — auto-rerunning
// would post a duplicate comment and double the Claude cost. The user
// re-runs an interrupted jira_ai manually (which is the deliberate
// "refinement" mode), so the boot must never resume it for them.
func SafeToResume(kind string) bool {
	switch kind {
	case "apt_upgrade", "docker_pull", "docker_compose_pull", "image_prune", "backup_now":
		return true
	default: // shell, jira_ai_analysis, unknown
		return false
	}
}

// scopeAlive reports whether a systemd transient scope unit is still active.
// Used by the boot reconcile to tell a DETACHED job that is still running in
// its own scope from one that died with the process. Returns false on any
// error (unit gone, systemctl missing) — fail closed so we reconcile a
// possibly-dead job rather than orphan a running one's status. Always false
// for an in-process job, whose j.Scope is empty.
func scopeAlive(scope string) bool {
	if scope == "" {
		return false
	}
	return exec.Command("systemctl", "is-active", "--quiet", scope).Run() == nil
}

// scopeStop stops a transient scope, killing everything in its cgroup (SIGTERM
// and, on timeout, SIGKILL). It is the ONLY way to cancel a DETACHED job: the
// work runs in another process, outside our cgroup, so no context.CancelFunc
// from here can reach it.
//
// Bounded wait: `systemctl stop` blocks until the unit dies, and a process
// that is slow to exit would tie up the HTTP handler. On timeout the client is
// cut loose but systemd carries on with the stop on its side — the job has
// been marked cancelled either way.
func scopeStop(scope string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "systemctl", "stop", scope).Run()
}

// pushPending enqueues an id for a worker without blocking. The channel is
// large (256); if it ever fills, fall back to a goroutine that blocks until
// a slot frees. Shared by Enqueue, Rerun and the boot reconcile so none of
// them can deadlock on a full channel.
func (q *Queue) pushPending(id string) {
	select {
	case q.pending <- id:
	default:
		go func() { q.pending <- id }()
	}
}

// NewQueue creates a queue rooted at opts.DataDir, loads any persisted state,
// and starts opts.Workers goroutines. Caller must invoke Shutdown on exit.
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
	// Reconcile state left behind by a crash/restart (deploy SIGTERM).
	//   - queued  → re-push for a worker (non-blocking; bare send here would
	//               deadlock if len(pending)>256).
	//   - running → if a DETACHED scope still owns it, leave it alone;
	//               else resume it when SafeToResume, otherwise mark it the
	//               HONEST terminal "interrupted" (recoverable via Rerun) —
	//               we don't blindly restart side-effecting jobs because we
	//               can't know what they wrote to disk before dying.
	for _, j := range q.jobs {
		switch j.Status {
		case StatusQueued:
			q.pushPending(j.ID)
		case StatusRunning:
			if j.Scope != "" {
				// DETACHED job. If its scope is still alive, the external
				// process owns the status — leave it; the reaper tracks it.
				if scopeAlive(j.Scope) {
					continue
				}
				// Scope dead: the detached process may have FINISHED while we
				// were down and written a terminal result. Adopt it — never
				// clobber a completed job as "interrupted".
				if ds, ok := readDetachedStatus(q.dataDir, j.ID); ok && isTerminal(ds.Status) {
					applyDetached(j, ds)
					continue
				}
				// Scope dead AND no terminal result → genuinely interrupted.
				j.Status = StatusInterrupted
				j.Error = "interrupted: vps-manager restart"
				j.Finished = time.Now().Unix()
				continue
			}
			if SafeToResume(j.Kind) {
				j.Status = StatusQueued
				j.Started, j.Progress = 0, 0
				q.pushPending(j.ID)
			} else {
				j.Status = StatusInterrupted
				j.Error = "interrupted: vps-manager restart"
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

// applyDetached overwrites a job's live fields from a detached status file.
// Progress only moves forward (the reaper can race a stale read). Caller holds
// q.mu (or owns j exclusively, as during NewQueue reconcile).
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

// detachReaper polls per-job status files for DETACHED jobs and merges them
// into the in-process view, so /api/queue (and the board strip) reflect a
// detached job's progress and eventual completion. It is the ONLY bridge
// between the detached writer and the main process; it never writes the
// detached file. Also rescues a detached job whose scope died without writing
// a terminal status (process crash) → marks it interrupted. Main-process only;
// not tracked by q.wg (it's a poller, not a job worker) — stops on shutdown.
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
		// A NON-terminal status is only valid while the scope is alive. Without
		// this check, a detached process killed abruptly (SIGKILL, OOM, an external
		// systemctl stop) left behind a file saying "running" that was re-applied
		// forever — the dead-scope branch below was never reached and the job stayed
		// "running" for good, with no process behind it at all.
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
			// Past the startup grace, scope gone, and the external process
			// never wrote a status file → it crashed before reporting. Mark
			// interrupted (recoverable). The grace avoids a false interrupt in
			// the window between launch and the first status write while
			// systemd-run is still spinning the scope up.
			j.Status = StatusInterrupted
			j.Error = "interrupted: detached process vanished"
			j.Finished = time.Now().Unix()
			changed = true
		}
		terminal := isTerminal(j.Status)
		if changed {
			q.persistLocked()
		}
		// [terminal-hook 5/5]: the reaper adopts a detached job's final
		// result (done/failed) or marks a vanished one interrupted — a terminal
		// transition that happens at RUNTIME (not boot), so it SHOULD notify.
		// Guarded by `terminal && changed` so a mere progress update doesn't
		// fire, and the set-once flag makes a re-reaped job notify only once.
		// DO NOT REMOVE.
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

// Register attaches a runner. Must be called before NewRouter starts
// serving requests for the kind.
func (q *Queue) Register(r Runner) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.runners[r.Kind()] = r
}

// SetDetach marks kinds to run DETACHED via launcher instead of the in-process
// worker pool. launcher starts the external process and returns the systemd
// scope unit name. If launcher returns an error at enqueue time, the job falls
// back to in-process execution — detach is a survivability bonus, never a hard
// dependency. Call once at boot, before serving requests.
func (q *Queue) SetDetach(launcher func(id string) (string, error), kinds ...string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.detachLauncher = launcher
	q.detachKinds = make(map[string]bool, len(kinds))
	for _, k := range kinds {
		q.detachKinds[k] = true
	}
}

// SetNotifier wires the terminal-job hook. MUST be called once at boot
// AFTER NewQueue returns: the boot reconcile runs inside NewQueue with the
// notifier still nil, so a job reconciled as interrupted/adopted at boot does
// NOT spam a notification on every restart. fn receives a COPY of the job and
// must be non-blocking (notify.Router.Dispatch is).
func (q *Queue) SetNotifier(fn func(*Job)) {
	q.mu.Lock()
	q.notifier = fn
	q.mu.Unlock()
}

// notifyTerminalLocked fires the terminal-job hook exactly once per job. ALWAYS
// called with q.mu held (its name carries the contract). Idempotent via the
// set-once j.notifiedTerminal guard.
//
// Two guards make the restart story correct:
//   - notifier == nil → no-op AND we DON'T set the flag. This covers the boot
//     window (between r.queue=q and SetNotifier) and detach-disabled builds; a
//     job finishing then simply isn't notified, with no loss of future dedup.
//   - already-notified or non-terminal → no-op. Combined with the flag being
//     json-invisible (lost on restart) this is still safe: boot reconcile never
//     calls this, and a job already terminal in state.json is never re-written
//     through a runtime site, so it never re-notifies.
//
// notify.Router.Dispatch is non-blocking and never touches q.mu, so invoking
// the notifier directly under the lock is safe and contention-free.
func (q *Queue) notifyTerminalLocked(j *Job) {
	if q.notifier == nil {
		return
	}
	if !isTerminal(j.Status) || j.notifiedTerminal {
		return
	}
	j.notifiedTerminal = true
	jc := *j // copy: the handler must not retain a pointer into queue state
	q.notifier(&jc)
}

// Enqueue stores the job and returns the assigned id. The handler should
// have already done the authz check via AuthorizedFor; this method does
// not re-check (the runner registry might not know enough context).
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

// dispatch starts a queued/reset job: DETACHED in its own systemd scope when
// the kind opts in and the launcher succeeds, else on the in-process worker
// pool. Shared by Enqueue and Rerun so both treat detach kinds identically.
// Caller must NOT hold q.mu.
func (q *Queue) dispatch(j *Job) {
	q.mu.Lock()
	detach := q.detachKinds[j.Kind] && q.detachLauncher != nil
	launcher := q.detachLauncher
	q.mu.Unlock()

	if detach {
		// Clear any stale per-job status file from a previous run of this id
		// (Rerun reuses the id) so the reaper can't adopt an old terminal
		// status before the fresh external process writes its first one.
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
		// launcher unavailable/failed → in-process fallback. Clear Scope so
		// the reaper doesn't treat this in-process run as detached.
		q.mu.Lock()
		j.Scope = ""
		q.persistLocked()
		q.mu.Unlock()
	}
	q.pushPending(j.ID)
}

// snapshot returns a cancel-free copy of a job, or nil if gone.
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

// Get returns a snapshot of one job.
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

// List returns all jobs matching the filter, newest first. Empty filter
// returns everything (still bounded by maxKeep).
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

// ListBySource returns jobs whose Source is in the given set, newest first
// (capped at limit). Used by the scheduler's per-job run history.
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

// Cancel signals the job context and marks the job cancelled.
// Returns ErrNotFound if id unknown; no-op if already finished.
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
	// A DETACHED job runs in a systemd scope, in another process: j.cancel is
	// nil (dispatch never called runOne for it) and no ctx from here reaches
	// it. Without this branch, cancelling a jira_ai_analysis was a silent no-op
	// — Cancel returned nil and the UI showed success while the job ran on.
	scope := ""
	if j.Scope != "" && j.Status == StatusRunning {
		scope = j.Scope
	}
	if j.Status == StatusQueued || scope != "" {
		j.Status = StatusCancelled
		j.Finished = time.Now().Unix()
		q.persistLocked()
		// [terminal-hook 4/5]: cancelling a QUEUED job is terminal right here
		// (it never enters runOne). A RUNNING in-process job, by contrast, gets
		// ctx-cancel and reaches its terminal state through hook 2/5 in runOne's
		// switch — which is why this hook covers only the queued branch, so we
		// don't notify twice. DO NOT REMOVE.
		//
		// The DETACHED branch joins in for the same reason as queued: there is no
		// runOne in this process to close it out, so the hook has to fire here.
		q.notifyTerminalLocked(j)
	}
	q.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if scope != "" {
		// Marking it Cancelled above already takes the job off the reaper's radar
		// (it only looks at Status==Running), so the stale status file from the
		// detached process can no longer resurrect it as running.
		_ = scopeStop(scope)
	}
	q.broadcast(id, Event{Type: "status", JobID: id, Status: StatusCancelled, TS: time.Now().Unix()})
	return nil
}

// Delete removes a finished job from history and deletes its log file.
// Returns ErrNotFound if id unknown, ErrConflict if the job is still
// queued/running (cancel it first — we never kill an active job out from
// under its worker). Idempotent only for unknown ids via ErrNotFound.
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
	// q.order is append-only history; rebuild without this id.
	out := q.order[:0]
	for _, oid := range q.order {
		if oid != id {
			out = append(out, oid)
		}
	}
	q.order = out
	if j.LogPath != "" {
		if err := os.Remove(j.LogPath); err != nil && !os.IsNotExist(err) {
			// best-effort: a leftover log file shouldn't block deletion of
			// the record. State is already mutated; persist below.
		}
	}
	_ = os.Remove(detachedPath(q.dataDir, id)) // best-effort: clean detached status file
	q.persistLocked()
	return nil
}

// Rerun restarts the SAME job in place: it keeps the id, kind, args, owner
// and source, resets run state (status→queued, progress/error/timestamps
// cleared) and re-queues it for a worker. The old log is truncated when the
// worker reopens LogPath via os.Create. Returns ErrNotFound if id unknown,
// ErrConflict if the job is still queued/running (nothing to restart).
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
	j.Scope = "" // drop any stale detached scope; dispatch re-decides below
	j.Queued = time.Now().Unix()
	j.cancel = nil
	queuedTS := j.Queued
	q.persistLocked()
	q.mu.Unlock()

	q.broadcast(id, Event{Type: "status", JobID: id, Status: StatusQueued, TS: queuedTS})
	q.dispatch(j) // detach kinds re-launch detached; others go in-process
	cp := q.snapshot(id)
	if cp == nil {
		cp = j
	}
	return cp, nil
}

// Subscribe returns a channel that receives live events for jobID. The
// caller must call the returned cancel to free resources. The channel is
// buffered; slow consumers drop messages.
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

// ReadLog returns the persisted log (up to maxBytes from the end).
// 0 means "all".
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

// Shutdown stops workers and persists state. Existing running jobs are
// signalled to cancel. It blocks until the workers are done and then does the
// final persist — without that, the in-flight job's j.Status/Finished could
// live only in memory and be lost on restart.
// Shutdown stops accepting work and waits — bounded — for running jobs to
// drain. Jobs that finish within the deadline persist their real terminal
// status; jobs still running when the cap expires are marked the HONEST
// StatusInterrupted so the next boot can offer them for re-run instead of
// silently losing them. The cap is min(4s, ctx remaining): the systemd unit
// gives a 10s TimeoutStopSec and main.go budgets 15s total across all
// subsystems, so the queue must not hog the whole window.
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

	// Wait for workers to see the cancel and mark jobs terminal, but never
	// longer than the cap — a job ignoring its ctx must not wedge shutdown.
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
			// DETACHED jobs (Scope set) survive the restart in their own
			// systemd scope — do NOT mark them interrupted. The boot reconcile
			// + reaper pick them back up.
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

// Counts returns the number of jobs a restart would INTERRUPT: in-process
// running jobs (Scope == "") plus queued jobs. DETACHED running jobs (Scope
// set) are excluded — they live in their own systemd scope and survive the
// restart, so the deploy drain must not wait on them. Used by /api/health for
// the deploy drain gate and by the Operations UI to show in-process depth.
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

// --- internals ---

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
		// [terminal-hook 1/5]: unknown-kind path is terminal but does
		// NOT broadcast — a hook placed only after the switch below would miss
		// it. Fire here, under q.mu, before Unlock. DO NOT REMOVE.
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

	// Tee writes to both the file and a live broadcaster.
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
	// [terminal-hook 2/5]: the normal runOne exit (done/failed/cancelled
	// via ctx-cancel). Under `defer q.mu.Unlock()`. DO NOT REMOVE.
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
	// [terminal-hook 3/5]: fail() is reached only from runOne (log
	// create failed) — a real terminal that bypasses the switch. Under
	// `defer q.mu.Unlock()`. DO NOT REMOVE.
	q.notifyTerminalLocked(j)
}

func (q *Queue) broadcast(jobID string, ev Event) {
	q.listMu.Lock()
	defer q.listMu.Unlock()
	for _, ch := range q.listeners[jobID] {
		select {
		case ch <- ev:
		default: // slow consumer; drop
		}
	}
}

// pruneLocked trims oldest finished jobs once order exceeds maxKeep.
// Caller must hold q.mu.
func (q *Queue) pruneLocked() {
	if len(q.order) <= q.maxKeep {
		return
	}
	// drop from the front while the job is finished (don't drop running)
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

// load reads state.json. Missing file is fine — empty start.
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

// persistLocked snapshot serialises to state.json atomically. Caller must
// hold q.mu.
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
	// fsync before the rename: this is what guarantees post-crash durability. A
	// POSIX rename is atomic, but without fsync the bytes may not have reached
	// the disk before rename updates the inode -> after a crash we recover an
	// empty or partially written file.
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

// broadcastWriter is io.Writer that emits a log event for each Write call.
// File persistence is handled separately via MultiWriter so log lines
// survive crashes even if no listener is connected.
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
