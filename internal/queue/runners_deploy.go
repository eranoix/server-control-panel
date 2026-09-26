package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"server-control-panel/internal/deploy"
)

// AppDeployRunner runs a PaaS deploy through the queue — the path taken by the
// UI and by rollback. The `git push` trigger does NOT come through here: the
// hook calls the core deploy.Deploy directly (via vpsmctl), streaming to the
// git client. Both paths share deploy.Deploy, so the behaviour is identical.
type AppDeployRunner struct {
	DataDir string
}

// AppDeployArgs is the payload of the app_deploy job.
type AppDeployArgs struct {
	App     string `json:"app"`
	Ref     string `json:"ref,omitempty"`
	Commit  string `json:"commit,omitempty"`
	Preview string `json:"preview,omitempty"`
	Trigger string `json:"trigger,omitempty"`
}

func (AppDeployRunner) Kind() string { return "app_deploy" }

// AuthorizedFor: a deploy touches containers/nginx/FS — primary only, the same
// as the restart/backup runners.
func (AppDeployRunner) AuthorizedFor(_ string, isPrimary bool) bool { return isPrimary }

func (r AppDeployRunner) Run(ctx context.Context, args json.RawMessage, logW io.Writer, progress func(int), step func(string)) error {
	var a AppDeployArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return fmt.Errorf("invalid args: %w", err)
	}
	if a.App == "" {
		return fmt.Errorf("app required")
	}
	trigger := a.Trigger
	if trigger == "" {
		trigger = "ui"
	}
	if a.Preview != "" {
		step("preview " + a.Preview)
	} else {
		step("deploy " + a.App)
	}
	progress(10)
	st := deploy.Open(r.DataDir)
	_, err := deploy.Deploy(ctx, st, deploy.Spec{
		App: a.App, Ref: a.Ref, Commit: a.Commit, Preview: a.Preview, Trigger: trigger,
	}, logW)
	if err != nil {
		return err
	}
	progress(100)
	return nil
}

// DeployPreviewReapRunner tears down previews past their TTL. Scheduled (e.g.
// hourly); idempotent and cheap when there is nothing to clean up.
type DeployPreviewReapRunner struct {
	DataDir string
}

func (DeployPreviewReapRunner) Kind() string                                { return "deploy_preview_reap" }
func (DeployPreviewReapRunner) AuthorizedFor(_ string, isPrimary bool) bool { return isPrimary }

func (r DeployPreviewReapRunner) Run(ctx context.Context, _ json.RawMessage, logW io.Writer, progress func(int), step func(string)) error {
	step("sweeping previews")
	st := deploy.Open(r.DataDir)
	n, err := deploy.ReapPreviews(ctx, st, logW)
	if err != nil {
		return err
	}
	fmt.Fprintf(logW, "previews torn down: %d\n", n)
	progress(100)
	return nil
}
