// Package sdui_test wires the six Docker screens into the sdui package's own
// golden-fixture test binary — same rationale as scheduler_golden_test.go's
// header comment: golden_test.go's harness only sees screens registered
// inside the SAME test binary process, and being an external test package
// (sdui_test) is what lets this file import internal/mobilebff/screens
// without an import cycle.
package sdui_test

import (
	"context"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"

	docksvc "server-control-panel/internal/docker"
	"server-control-panel/internal/mobilebff/screens"
)

// dockerGoldenBackend is a small in-memory stand-in for internal/api.Router's
// *docker.Client — enough to build all six Docker screens and their rows
// deterministically for the golden corpus. It never touches a real Docker
// daemon; the golden harness only calls Build (via the sdui.Screen builder),
// never RunAction, so the mutating closures below are unreachable from the
// harness and exist only to satisfy DockerDeps' shape.
type dockerGoldenBackend struct{}

func (dockerGoldenBackend) deps() screens.DockerDeps {
	return screens.DockerDeps{
		ListContainers: func(context.Context) ([]types.Container, error) {
			return []types.Container{
				{ID: "c-golden-web", Names: []string{"/web"}, Image: "nginx:latest", State: "running", Created: 1798000000},
				{ID: "c-golden-db", Names: []string{"/db"}, Image: "postgres:16", State: "exited", Created: 1798000000},
			}, nil
		},
		StartContainer:   func(context.Context, string) error { return nil },
		StopContainer:    func(context.Context, string) error { return nil },
		RestartContainer: func(context.Context, string) error { return nil },
		RemoveContainer:  func(context.Context, string, bool) error { return nil },

		ListImages: func(context.Context) ([]image.Summary, error) {
			return []image.Summary{
				{ID: "sha256:golden1", RepoTags: []string{"nginx:latest"}, Size: 142606336, Created: 1798000000},
			}, nil
		},
		RemoveImage: func(context.Context, string, bool) error { return nil },

		ListVolumes: func(context.Context) (volume.ListResponse, error) {
			return volume.ListResponse{
				Volumes: []*volume.Volume{
					{Name: "golden-data", Driver: "local", UsageData: &volume.UsageData{Size: 1073741824, RefCount: 1}},
				},
			}, nil
		},

		ListNetworks: func(context.Context) ([]network.Summary, error) {
			return []network.Summary{
				{ID: "net-golden-bridge", Name: "bridge", Driver: "bridge", Scope: "local"},
			}, nil
		},

		ListComposeStacks: func(context.Context) ([]docksvc.ComposeProject, error) {
			return []docksvc.ComposeProject{
				{Name: "golden-stack", WorkingDir: "/opt/stacks/golden-stack", Status: "running"},
			}, nil
		},
		ComposeUp:   func(context.Context, string) (string, error) { return "up ok", nil },
		ComposeDown: func(context.Context, string) (string, error) { return "down ok", nil },

		Prune: func(context.Context, []string) (map[string]any, error) { return map[string]any{}, nil },

		AuditEvent: func(_, _, _ string) {},
	}
}

// init registers all six Docker screens into this test binary's
// process-global sdui registries exactly once — the same RegisterDocker(deps)
// internal/api/api.go calls in production, fed synthetic data instead of a
// real *docker.Client. This is what makes RegisteredScreens() (used by
// TestGoldenScreens and its two role-omission checks) see docker.containers,
// docker.images, docker.volumes, docker.networks, docker.compose and
// docker.prune at all when running `go test ./internal/mobilebff/sdui/...`.
func init() {
	screens.RegisterDocker(dockerGoldenBackend{}.deps())
}
