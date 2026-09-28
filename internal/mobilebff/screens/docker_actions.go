package screens

import (
	"context"
	"encoding/json"

	"server-control-panel/internal/mobilebff/sdui"
)

const (
	dockerActionContainerStart   = "docker.container.start"
	dockerActionContainerStop    = "docker.container.stop"
	dockerActionContainerRestart = "docker.container.restart"
	dockerActionContainerRemove  = "docker.container.remove"

	dockerActionImageRemove = "docker.image.remove"

	dockerActionComposeUp   = "docker.compose.up"
	dockerActionComposeDown = "docker.compose.down"

	dockerActionPruneRun = "docker.prune.run"
)

func dockerAdminViewer(v sdui.Viewer) bool { return v.IsAdmin() }

func registerDockerActions(deps DockerDeps) {
	sdui.RegisterAction(
		sdui.ActionDescriptor{
			ActionID:   dockerActionContainerStart,
			Method:     "POST",
			Endpoint:   "/api/mobile/v1/actions/" + dockerActionContainerStart,
			Permission: "authenticated",
		},
		authenticatedViewer,
		handleDockerContainerLifecycle(deps, deps.StartContainer, "docker.container.start"),
	)
	sdui.RegisterAction(
		sdui.ActionDescriptor{
			ActionID:   dockerActionContainerStop,
			Method:     "POST",
			Endpoint:   "/api/mobile/v1/actions/" + dockerActionContainerStop,
			Permission: "authenticated",
		},
		authenticatedViewer,
		handleDockerContainerLifecycle(deps, deps.StopContainer, "docker.container.stop"),
	)
	sdui.RegisterAction(
		sdui.ActionDescriptor{
			ActionID:   dockerActionContainerRestart,
			Method:     "POST",
			Endpoint:   "/api/mobile/v1/actions/" + dockerActionContainerRestart,
			Permission: "authenticated",
		},
		authenticatedViewer,
		handleDockerContainerLifecycle(deps, deps.RestartContainer, "docker.container.restart"),
	)
	sdui.RegisterAction(
		sdui.ActionDescriptor{
			ActionID:    dockerActionContainerRemove,
			Method:      "POST",
			Endpoint:    "/api/mobile/v1/actions/" + dockerActionContainerRemove,
			Permission:  "admin",
			Destructive: true,
		},
		dockerAdminViewer,
		handleDockerContainerRemove(deps),
	)

	sdui.RegisterAction(
		sdui.ActionDescriptor{
			ActionID:    dockerActionImageRemove,
			Method:      "POST",
			Endpoint:    "/api/mobile/v1/actions/" + dockerActionImageRemove,
			Permission:  "admin",
			Destructive: true,
		},
		dockerAdminViewer,
		handleDockerImageRemove(deps),
	)

	sdui.RegisterAction(
		sdui.ActionDescriptor{
			ActionID:   dockerActionComposeUp,
			Method:     "POST",
			Endpoint:   "/api/mobile/v1/actions/" + dockerActionComposeUp,
			Permission: "authenticated",
		},
		authenticatedViewer,
		handleDockerComposeUp(deps),
	)
	sdui.RegisterAction(
		sdui.ActionDescriptor{
			ActionID:    dockerActionComposeDown,
			Method:      "POST",
			Endpoint:    "/api/mobile/v1/actions/" + dockerActionComposeDown,
			Permission:  "admin",
			Destructive: true,
		},
		dockerAdminViewer,
		handleDockerComposeDown(deps),
	)

	sdui.RegisterAction(
		sdui.ActionDescriptor{
			ActionID:                 dockerActionPruneRun,
			Method:                   "POST",
			Endpoint:                 "/api/mobile/v1/actions/" + dockerActionPruneRun,
			Permission:               "admin",
			Destructive:              true,
			RequireTypedConfirmation: "PRUNE",
		},
		dockerAdminViewer,
		handleDockerPruneRun(deps),
	)
}

func findDockerContainerRow(ctx context.Context, deps DockerDeps, id string) (map[string]any, bool) {
	list, err := deps.ListContainers(ctx)
	if err != nil {
		return nil, false
	}
	for _, c := range list {
		if c.ID == id {
			return dockerContainerRow(c), true
		}
	}
	return nil, false
}

func handleDockerContainerLifecycle(deps DockerDeps, call func(ctx context.Context, id string) error, auditAction string) sdui.ActionHandler {
	return func(ctx context.Context, v sdui.Viewer, params map[string]string, _ json.RawMessage) (sdui.ActionResult, error) {
		id := params["id"]
		if id == "" {
			return sdui.ActionResult{}, sdui.ErrActionNotFound
		}
		if err := call(ctx, id); err != nil {
			return sdui.ActionResult{}, err
		}
		if deps.AuditEvent != nil {
			deps.AuditEvent(v.Username, auditAction, id)
		}
		if row, ok := findDockerContainerRow(ctx, deps, id); ok {
			return sdui.ActionResult{Patch: row}, nil
		}
		return sdui.ActionResult{Invalidate: []string{"containers-table"}}, nil
	}
}

type dockerRemoveInput struct {
	Force bool `json:"force"`
}

func handleDockerContainerRemove(deps DockerDeps) sdui.ActionHandler {
	return func(ctx context.Context, v sdui.Viewer, params map[string]string, input json.RawMessage) (sdui.ActionResult, error) {
		id := params["id"]
		if id == "" {
			return sdui.ActionResult{}, sdui.ErrActionNotFound
		}
		var in dockerRemoveInput
		if len(input) > 0 {
			if err := json.Unmarshal(input, &in); err != nil {
				return sdui.ActionResult{}, sdui.FieldErrors{}.Add("force", "invalid request body")
			}
		}
		if err := deps.RemoveContainer(ctx, id, in.Force); err != nil {
			return sdui.ActionResult{}, err
		}
		if deps.AuditEvent != nil {
			deps.AuditEvent(v.Username, "docker.container.remove", id)
		}
		return sdui.ActionResult{Invalidate: []string{"containers-table"}}, nil
	}
}

func handleDockerImageRemove(deps DockerDeps) sdui.ActionHandler {
	return func(ctx context.Context, v sdui.Viewer, params map[string]string, input json.RawMessage) (sdui.ActionResult, error) {
		id := params["id"]
		if id == "" {
			return sdui.ActionResult{}, sdui.ErrActionNotFound
		}
		var in dockerRemoveInput
		if len(input) > 0 {
			if err := json.Unmarshal(input, &in); err != nil {
				return sdui.ActionResult{}, sdui.FieldErrors{}.Add("force", "invalid request body")
			}
		}
		if err := deps.RemoveImage(ctx, id, in.Force); err != nil {
			return sdui.ActionResult{}, err
		}
		if deps.AuditEvent != nil {
			deps.AuditEvent(v.Username, "docker.image.remove", id)
		}
		return sdui.ActionResult{Invalidate: []string{"images-table"}}, nil
	}
}

func handleDockerComposeUp(deps DockerDeps) sdui.ActionHandler {
	return func(ctx context.Context, v sdui.Viewer, params map[string]string, _ json.RawMessage) (sdui.ActionResult, error) {
		stack := params["id"]
		if stack == "" {
			return sdui.ActionResult{}, sdui.ErrActionNotFound
		}
		if _, err := deps.ComposeUp(ctx, stack); err != nil {
			return sdui.ActionResult{}, err
		}
		if deps.AuditEvent != nil {
			deps.AuditEvent(v.Username, "docker.compose.up", stack)
		}
		return sdui.ActionResult{Invalidate: []string{"compose-table"}}, nil
	}
}

func handleDockerComposeDown(deps DockerDeps) sdui.ActionHandler {
	return func(ctx context.Context, v sdui.Viewer, params map[string]string, _ json.RawMessage) (sdui.ActionResult, error) {
		stack := params["id"]
		if stack == "" {
			return sdui.ActionResult{}, sdui.ErrActionNotFound
		}
		if _, err := deps.ComposeDown(ctx, stack); err != nil {
			return sdui.ActionResult{}, err
		}
		if deps.AuditEvent != nil {
			deps.AuditEvent(v.Username, "docker.compose.down", stack)
		}
		return sdui.ActionResult{Invalidate: []string{"compose-table"}}, nil
	}
}

type dockerPruneInput struct {
	Containers bool `json:"containers"`
	Images     bool `json:"images"`
	Volumes    bool `json:"volumes"`
	BuildCache bool `json:"build_cache"`
}

func handleDockerPruneRun(deps DockerDeps) sdui.ActionHandler {
	return func(ctx context.Context, v sdui.Viewer, _ map[string]string, input json.RawMessage) (sdui.ActionResult, error) {
		var in dockerPruneInput
		if len(input) > 0 {
			if err := json.Unmarshal(input, &in); err != nil {
				return sdui.ActionResult{}, sdui.FieldErrors{}.Add("containers", "invalid request body")
			}
		}

		var kinds []string
		if in.Containers {
			kinds = append(kinds, "containers")
		}
		if in.Images {
			kinds = append(kinds, "images")
		}
		if in.Volumes {
			kinds = append(kinds, "volumes")
		}
		if in.BuildCache {
			kinds = append(kinds, "build_cache")
		}
		if len(kinds) == 0 {
			return sdui.ActionResult{}, sdui.FieldErrors{}.Add("containers", "select at least one category to clean up")
		}

		result, err := deps.Prune(ctx, kinds)
		if err != nil {
			return sdui.ActionResult{}, err
		}
		if deps.AuditEvent != nil {
			deps.AuditEvent(v.Username, "docker.prune.run", joinDockerKinds(kinds))
		}
		return sdui.ActionResult{Patch: result}, nil
	}
}

func joinDockerKinds(kinds []string) string {
	out := ""
	for i, k := range kinds {
		if i > 0 {
			out += ","
		}
		out += k
	}
	return out
}
