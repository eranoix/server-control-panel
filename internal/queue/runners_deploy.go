package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"server-control-panel/internal/deploy"
)

type AppDeployRunner struct {
	DataDir string
}

type AppDeployArgs struct {
	App     string `json:"app"`
	Ref     string `json:"ref,omitempty"`
	Commit  string `json:"commit,omitempty"`
	Preview string `json:"preview,omitempty"`
	Trigger string `json:"trigger,omitempty"`
}

func (AppDeployRunner) Kind() string { return "app_deploy" }

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
