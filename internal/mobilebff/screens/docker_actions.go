package screens

import (
	"context"
	"encoding/json"

	"server-control-panel/internal/mobilebff/sdui"
)

// Action ids the six Docker screens reference from their tables' row_actions
// and the prune form's submit_action. There is deliberately no
// docker.volume.remove or docker.network.remove: internal/docker has no
// single-item delete for either anywhere (see deps.go's DockerDeps doc
// comment and docker.go's buildDockerVolumesScreen/buildDockerNetworksScreen).
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

// dockerAdminViewer is the RegisterAction authorize gate for every
// destructive Docker action: container.remove, image.remove,
// compose.down, prune.run. A non-admin invocation — even one that bypasses
// the missing UI affordance by calling the endpoint directly — gets
// ErrActionNotFound at RunAction's step 2, before the handler ever runs; see
// actionregistry.go's RunAction doc comment for why that is the same error
// as "action does not exist" rather than a 403.
func dockerAdminViewer(v sdui.Viewer) bool { return v.IsAdmin() }

// registerDockerActions registers the eight Docker mutations: three
// non-destructive container lifecycle actions plus remove, image remove,
// compose up/down, and prune. Called once by RegisterDocker (docker.go).
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
			// RequireTypedConfirmation deliberately empty: a container is
			// recreatable from its image/compose, the same reasoning as
			// scheduler.job.delete — Destructive:true on its own is already
			// proportionate.
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

// findDockerContainerRow re-lists containers and returns the row for id —
// used to build a Patch response after a lifecycle mutation.
// docker.Client/DockerDeps has no single-container fetch keyed by id (only
// the bulk list any table row already comes from), so the safest way to
// return the CURRENT state after start/stop/restart is the same list call
// the table itself uses, filtered down to one row — never re-deriving
// container state from the request that triggered the mutation.
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

// handleDockerContainerLifecycle implements the shared shape of
// start/stop/restart: call the domain closure, audit, then return a Patch
// with the container's new row state (falling back to Invalidate the whole
// table if the container disappeared or the re-list failed, which is still
// a correct client instruction — just a coarser one).
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

// dockerRemoveInput is the body docker.container.remove/docker.image.remove
// decode from ActionHandler's input — Force mirrors handlers_docker.go's
// ?force=1 query parameter for the same two operations, kept as the same
// client-controllable knob the web panel already exposes to any
// authenticated user, restricted here to admin by the action's authorize
// gate.
type dockerRemoveInput struct {
	Force bool `json:"force"`
}

// handleDockerContainerRemove implements docker.container.remove. Destructive
// confirmation itself is enforced by sdui.RunAction before this handler ever
// runs (see actionregistry.go).
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

// handleDockerImageRemove implements docker.image.remove.
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

// handleDockerComposeUp implements docker.compose.up. params["id"] is the
// compose project/stack name — every row action in this package reads the
// clicked row's "id" field (see dockerComposeRow), never a
// screen-specific param key.
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

// handleDockerComposeDown implements docker.compose.down. Destructive
// confirmation itself is enforced by sdui.RunAction before this handler ever
// runs.
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

// dockerPruneInput is the body docker.prune.run decodes from ActionHandler's
// input — one bool per prune-form field (docker.go's buildDockerPruneScreen).
type dockerPruneInput struct {
	Containers bool `json:"containers"`
	Images     bool `json:"images"`
	Volumes    bool `json:"volumes"`
	BuildCache bool `json:"build_cache"`
}

// handleDockerPruneRun implements docker.prune.run — the plan's designated
// "early destructive proof": Destructive:true plus RequireTypedConfirmation
// (docker_actions.go's RegisterAction call) means deps.Prune is unreachable
// without BOTH a confirmed round trip AND the typed "PRUNE" string (see
// actionregistry.go's RunAction steps 3-4). This handler adds its own
// content validation on top: an empty kind selection is a form-level error,
// never a call to deps.Prune with an implicit "everything".
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

// joinDockerKinds renders the selected prune kinds as one audit target
// string (e.g. "containers,images") without pulling in the "strings"
// package for a single call site.
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
