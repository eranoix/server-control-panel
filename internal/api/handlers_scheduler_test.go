package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/config"
	"server-control-panel/internal/queue"
	"server-control-panel/internal/scheduler"
)

type apiFakeEnq struct{ n int }

func (f *apiFakeEnq) Enqueue(kind string, _ json.RawMessage, owner, source string) (string, error) {
	f.n++
	return "q1", nil
}

type stubRunner struct {
	kind  string
	authz bool
}

func (s stubRunner) Kind() string                    { return s.kind }
func (s stubRunner) AuthorizedFor(string, bool) bool { return s.authz }
func (s stubRunner) Run(context.Context, json.RawMessage, io.Writer, func(int), func(string)) error {
	return nil
}

func newSchedTestRouter(t *testing.T) *Router {
	t.Helper()
	dir := t.TempDir()
	r := &Router{cfg: &config.Config{DataDir: dir, Primary: "sam"}}
	r.queueRunners = map[string]queue.Runner{}
	reg := func(run queue.Runner) { r.queueRunners[run.Kind()] = run }
	reg(queue.AptUpgradeRunner{})
	reg(queue.DockerPullRunner{})
	reg(queue.DockerComposePullRunner{})
	reg(queue.ImagePruneRunner{})
	reg(queue.BackupNowRunner{DataDir: dir})
	reg(queue.ShellRunner{})
	reg(queue.DockerRestartRunner{})
	reg(queue.DockerComposeRestartRunner{})
	reg(queue.SystemdRestartRunner{})
	reg(queue.DockerPruneRunner{})
	reg(queue.HTTPCheckRunner{})
	reg(queue.SSLCheckRunner{})
	reg(queue.DiskCheckRunner{})
	reg(queue.SecurityAuditRunner{})
	reg(queue.RootkitScanRunner{})
	reg(queue.IntegrityCheckRunner{})
	reg(queue.TrivyScanRunner{})
	reg(queue.Fail2banReportRunner{})
	reg(queue.AuditReportRunner{})
	reg(queue.CleanupRunner{})
	reg(queue.CertRenewRunner{})
	reg(queue.RcloneSyncRunner{})
	reg(queue.DockerComposeUpRunner{})
	reg(queue.GitPullRunner{})
	reg(queue.AptUpdateCheckRunner{})
	reg(queue.RebootRunner{})
	reg(queue.DBBackupRunner{DataDir: dir})
	reg(queue.WatchdogRunner{DataDir: dir})
	reg(queue.SessionBackupRunner{Backup: r.runSessionBackupJob})
	reg(queue.AgentRoutineRunner{Spawn: r.runAgentRoutineJob})
	reg(stubRunner{kind: "jira_ai_analysis", authz: true})

	sc, err := scheduler.New(filepath.Join(dir, "scheduler", "jobs.json"), &apiFakeEnq{})
	if err != nil {
		t.Fatal(err)
	}
	r.scheduler = sc
	return r
}

func schedReq(method, target, user, body string) *http.Request {
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
	} else {
		req = httptest.NewRequest(method, target, nil)
	}
	return req.WithContext(auth.WithUser(context.Background(), user))
}

func catalogKinds(t *testing.T, r *Router, user string) []string {
	t.Helper()
	rec := httptest.NewRecorder()
	r.handleSchedulerCatalog(rec, schedReq(http.MethodGet, "/api/scheduler/catalog", user, ""))
	if rec.Code != 200 {
		t.Fatalf("catalog %s: status=%d body=%q", user, rec.Code, rec.Body.String())
	}
	var out struct {
		Kinds     []schedDescriptor `json:"kinds"`
		IsPrimary bool              `json:"is_primary"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("catalog decode: %v (%q)", err, rec.Body.String())
	}
	ks := make([]string, 0, len(out.Kinds))
	for _, d := range out.Kinds {
		if !d.Schedulable {
			t.Errorf("catalogue leaked non-schedulable kind %q", d.Kind)
		}
		ks = append(ks, d.Kind)
	}
	return ks
}

func TestSchedulerCatalogPrimarySeesAllSchedulable(t *testing.T) {
	r := newSchedTestRouter(t)
	got := catalogKinds(t, r, "sam")
	want := []string{
		"agent_routine",
		"apt_updatecheck", "apt_upgrade", "audit_report", "backup_now", "cert_renew", "cleanup", "db_backup", "disk_check",
		"docker_compose_pull", "docker_compose_restart", "docker_compose_up", "docker_prune", "docker_pull", "docker_restart",
		"fail2ban_report", "git_pull", "http_check", "image_prune", "integrity_check",
		"rclone_sync", "reboot", "rootkit_scan", "security_audit", "session_backup", "shell", "ssl_check", "systemd_restart", "trivy_scan", "watchdog",
	}
	if len(got) != len(want) {
		t.Fatalf("primary catalogue = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("primary catalogue order/contents = %v, want %v", got, want)
		}
	}
}

func TestSchedulerCatalogNonPrimaryFiltered(t *testing.T) {
	r := newSchedTestRouter(t)
	got := catalogKinds(t, r, "jordan")
	want := []string{"docker_compose_pull", "docker_pull", "session_backup"}
	if len(got) != len(want) {
		t.Fatalf("non-primary catalogue = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("non-primary catalogue = %v, want %v", got, want)
		}
	}
}

func TestSchedulerCatalogAntiDrift(t *testing.T) {
	r := newSchedTestRouter(t)
	for kind := range r.queueRunners {
		if _, ok := schedDescriptors[kind]; !ok {
			t.Errorf("runner kind %q has no explicit descriptor (add it to schedDescriptors)", kind)
		}
	}
}

func TestSchedulerCreateDeniedForPrimaryKind(t *testing.T) {
	r := newSchedTestRouter(t)
	rec := httptest.NewRecorder()
	r.handleSchedulerJobs(rec, schedReq(http.MethodPost, "/api/scheduler/jobs", "jordan",
		`{"name":"x","schedule":"*/5 * * * *","kind":"shell","enabled":true}`))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-primary create shell: status=%d want 403 (body=%q)", rec.Code, rec.Body.String())
	}
}

func TestSchedulerCreateAllowedForOpenKind(t *testing.T) {
	r := newSchedTestRouter(t)
	rec := httptest.NewRecorder()
	r.handleSchedulerJobs(rec, schedReq(http.MethodPost, "/api/scheduler/jobs", "jordan",
		`{"name":"pull","schedule":"*/5 * * * *","kind":"docker_pull","enabled":true,"args":{"ref":"nginx:latest"}}`))
	if rec.Code != 200 {
		t.Fatalf("non-primary create docker_pull: status=%d want 200 (body=%q)", rec.Code, rec.Body.String())
	}
}

func TestSchedulerSessionBackupOwnerForcedServerSide(t *testing.T) {
	r := newSchedTestRouter(t)
	rec := httptest.NewRecorder()
	r.handleSchedulerJobs(rec, schedReq(http.MethodPost, "/api/scheduler/jobs", "jordan",
		`{"name":"bk","schedule":"0 * * * *","kind":"session_backup","enabled":true,"args":{"owner":"sam","session":"main"}}`))
	if rec.Code != 200 {
		t.Fatalf("create session_backup as jordan: status=%d want 200 (body=%q)", rec.Code, rec.Body.String())
	}
	var got scheduler.Job
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode job: %v", err)
	}
	var a queue.SessionBackupArgs
	if err := json.Unmarshal(got.Args, &a); err != nil {
		t.Fatalf("decode args: %v (%q)", err, string(got.Args))
	}
	if a.Owner != "jordan" {
		t.Fatalf("owner in args = %q, want %q (client forged sam)", a.Owner, "jordan")
	}
	if a.Session != "main" {
		t.Fatalf("session in args = %q, want %q (should not touch)", a.Session, "main")
	}
}

func TestSchedulerRunNowOrphanDenied(t *testing.T) {
	r := newSchedTestRouter(t)
	orphan, err := r.scheduler.Save(scheduler.Job{
		Name: "orphan", Schedule: "0 0 * * *", Kind: "shell", Owner: "jordan", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	r.handleSchedulerJobByID(rec, schedReq(http.MethodPost,
		"/api/scheduler/jobs/"+orphan.ID+"/run-now", "jordan", ""))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("run-now orphan shell as jordan: status=%d want 403 (body=%q)", rec.Code, rec.Body.String())
	}
}

func TestSchedulerRunNowPrimaryWorks(t *testing.T) {
	r := newSchedTestRouter(t)
	j, err := r.scheduler.Save(scheduler.Job{
		Name: "apt", Schedule: "0 0 * * *", Kind: "apt_upgrade", Owner: "sam", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	r.handleSchedulerJobByID(rec, schedReq(http.MethodPost,
		"/api/scheduler/jobs/"+j.ID+"/run-now", "sam", ""))
	if rec.Code != 200 {
		t.Fatalf("primary run-now apt_upgrade: status=%d want 200 (body=%q)", rec.Code, rec.Body.String())
	}
}
