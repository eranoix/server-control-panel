package screens

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/config"
	docksvc "server-control-panel/internal/docker"
	"server-control-panel/internal/httpx"
	"server-control-panel/internal/mobilebff"
	"server-control-panel/internal/mobilebff/sdui"
)

const (
	dockerContainersScreenID = "docker.containers"
	dockerImagesScreenID     = "docker.images"
	dockerVolumesScreenID    = "docker.volumes"
	dockerNetworksScreenID   = "docker.networks"
	dockerComposeScreenID    = "docker.compose"
	dockerPruneScreenID      = "docker.prune"
)

const (
	dockerContainersRowsEndpoint = mobilebff.Prefix + "/docker/containers"
	dockerImagesRowsEndpoint     = mobilebff.Prefix + "/docker/images"
	dockerVolumesRowsEndpoint    = mobilebff.Prefix + "/docker/volumes"
	dockerNetworksRowsEndpoint   = mobilebff.Prefix + "/docker/networks"
	dockerComposeRowsEndpoint    = mobilebff.Prefix + "/docker/compose"
)

const dockerTimestampFormat = "2006-01-02 15:04 UTC"

func RegisterDocker(deps DockerDeps) {
	sdui.Register(dockerContainersScreenID, func(_ context.Context, v sdui.Viewer) (*sdui.Envelope, error) {
		return buildDockerContainersScreen(v), nil
	})
	sdui.Register(dockerImagesScreenID, func(_ context.Context, v sdui.Viewer) (*sdui.Envelope, error) {
		return buildDockerImagesScreen(v), nil
	})
	sdui.Register(dockerVolumesScreenID, func(_ context.Context, _ sdui.Viewer) (*sdui.Envelope, error) {
		return buildDockerVolumesScreen(), nil
	})
	sdui.Register(dockerNetworksScreenID, func(_ context.Context, _ sdui.Viewer) (*sdui.Envelope, error) {
		return buildDockerNetworksScreen(), nil
	})
	sdui.Register(dockerComposeScreenID, func(_ context.Context, v sdui.Viewer) (*sdui.Envelope, error) {
		return buildDockerComposeScreen(v), nil
	})
	sdui.Register(dockerPruneScreenID, func(_ context.Context, v sdui.Viewer) (*sdui.Envelope, error) {
		return buildDockerPruneScreenForViewer(v)
	})

	sdui.RegisterCatalog(dockerContainersScreenID, sdui.GroupDocker, "Containers", alwaysVisible)
	sdui.RegisterCatalog(dockerImagesScreenID, sdui.GroupDocker, "Docker images", alwaysVisible)
	sdui.RegisterCatalog(dockerVolumesScreenID, sdui.GroupDocker, "Docker volumes", alwaysVisible)
	sdui.RegisterCatalog(dockerNetworksScreenID, sdui.GroupDocker, "Docker networks", alwaysVisible)
	sdui.RegisterCatalog(dockerComposeScreenID, sdui.GroupDocker, "Compose", alwaysVisible)
	sdui.RegisterCatalog(dockerPruneScreenID, sdui.GroupDocker, "Docker cleanup", adminOnly)

	registerDockerActions(deps)

	mobilebff.Register("docker.containers.rows", func(api huma.API, mbDeps mobilebff.Deps) {
		registerDockerContainersRows(api, deps, mbDeps)
	})
	mobilebff.Register("docker.images.rows", func(api huma.API, mbDeps mobilebff.Deps) {
		registerDockerImagesRows(api, deps, mbDeps)
	})
	mobilebff.Register("docker.volumes.rows", func(api huma.API, mbDeps mobilebff.Deps) {
		registerDockerVolumesRows(api, deps, mbDeps)
	})
	mobilebff.Register("docker.networks.rows", func(api huma.API, mbDeps mobilebff.Deps) {
		registerDockerNetworksRows(api, deps, mbDeps)
	})
	mobilebff.Register("docker.compose.rows", func(api huma.API, mbDeps mobilebff.Deps) {
		registerDockerComposeRows(api, deps, mbDeps)
	})

	sdui.RegisterForbiddenForNonAdmin(dockerContainersScreenID, func() []string {
		return []string{dockerActionContainerRemove}
	})
	sdui.RegisterForbiddenForNonAdmin(dockerImagesScreenID, func() []string {
		return []string{dockerActionImageRemove}
	})
	sdui.RegisterForbiddenForNonAdmin(dockerComposeScreenID, func() []string {
		return []string{dockerActionComposeDown}
	})
	sdui.RegisterForbiddenForNonAdmin(dockerPruneScreenID, func() []string {
		return []string{dockerActionPruneRun}
	})
}

func buildDockerContainersScreen(v sdui.Viewer) *sdui.Envelope {
	table := sdui.TableComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeTable, ID: "containers-table"},
		Columns: []sdui.TableColumn{
			{Key: "name", Label: "Name", Kind: "text"},
			{Key: "image", Label: "Image", Kind: "text"},
			{Key: "status", Label: "Status", Kind: "badge", BadgeMap: map[string]string{
				"running": "success", "restarting": "warning", "paused": "neutral",
				"exited": "neutral", "dead": "danger", "created": "neutral",
			}},
			{Key: "created", Label: "Created", Kind: "text"},
		},
		RowsSource: sdui.DataSource{Endpoint: dockerContainersRowsEndpoint},
		RowActions: []sdui.ActionRef{
			{ActionID: dockerActionContainerStart, Label: "Start", Style: "secondary"},
			{ActionID: dockerActionContainerStop, Label: "Stop", Style: "secondary"},
			{ActionID: dockerActionContainerRestart, Label: "Restart", Style: "secondary"},
			{ActionID: dockerActionContainerRemove, Label: "Remove", Style: "destructive"},
		},
		EmptyState: &sdui.EmptyState{Text: "Containers are the units Docker runs on this server; the list shows the running ones and the stopped ones too. Empty is unusual here, because the panel itself runs in a container — it usually means the stack was taken down. In Docker › Compose you can bring a whole stack back up."},
	}
	if !v.IsAdmin() {
		sdui.DropRowActions(&table, func(a sdui.ActionRef) bool { return a.ActionID != dockerActionContainerRemove })
	}

	confirm := sdui.ConfirmDestructiveComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeConfirmDestructive, ID: "container-remove-confirm"},
		ActionID:      dockerActionContainerRemove,
		Message:       "This container will be removed. If it is running, the removal fails unless 'force' is used.",
	}

	screen := sdui.Screen{ID: dockerContainersScreenID, Title: "Containers", Components: []sdui.Component{table}}
	if v.IsAdmin() {
		screen.Components = append(screen.Components, confirm)
	}
	return &sdui.Envelope{Screen: screen}
}

func buildDockerImagesScreen(v sdui.Viewer) *sdui.Envelope {
	table := sdui.TableComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeTable, ID: "images-table"},
		Columns: []sdui.TableColumn{
			{Key: "repo_tag", Label: "Repository:Tag", Kind: "text"},
			{Key: "size", Label: "Size", Kind: "text"},
			{Key: "created", Label: "Created", Kind: "text"},
		},
		RowsSource: sdui.DataSource{Endpoint: dockerImagesRowsEndpoint},
		RowActions: []sdui.ActionRef{
			{ActionID: dockerActionImageRemove, Label: "Remove", Style: "destructive"},
		},
		EmptyState: &sdui.EmptyState{Text: "Images are the template every container is born from; here is what has already been pulled or built on this server. With no image, no container starts — normally there are at least the ones of the running stack. They come back on their own with the next pull or build of a Compose stack."},
	}
	if !v.IsAdmin() {
		sdui.DropRowActions(&table, func(a sdui.ActionRef) bool { return a.ActionID != dockerActionImageRemove })
	}

	confirm := sdui.ConfirmDestructiveComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeConfirmDestructive, ID: "image-remove-confirm"},
		ActionID:      dockerActionImageRemove,
		Message:       "This image will be removed. If a container is using it, the removal fails unless 'force' is used.",
	}

	screen := sdui.Screen{ID: dockerImagesScreenID, Title: "Images", Components: []sdui.Component{table}}
	if v.IsAdmin() {
		screen.Components = append(screen.Components, confirm)
	}
	return &sdui.Envelope{Screen: screen}
}

func buildDockerVolumesScreen() *sdui.Envelope {
	table := sdui.TableComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeTable, ID: "volumes-table"},
		Columns: []sdui.TableColumn{
			{Key: "name", Label: "Name", Kind: "text"},
			{Key: "driver", Label: "Driver", Kind: "text"},
			{Key: "size", Label: "Size", Kind: "text"},
		},
		RowsSource: sdui.DataSource{Endpoint: dockerVolumesRowsEndpoint},
		EmptyState: &sdui.EmptyState{Text: "Volumes are the piece of disk that outlives the container: database, uploads, cache. Empty is normal when the stack only uses bind mounts of host folders. There is no way to create a volume here — it is born with the container that declares it."},
	}
	screen := sdui.Screen{ID: dockerVolumesScreenID, Title: "Volumes", Components: []sdui.Component{table}}
	return &sdui.Envelope{Screen: screen}
}

func buildDockerNetworksScreen() *sdui.Envelope {
	table := sdui.TableComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeTable, ID: "networks-table"},
		Columns: []sdui.TableColumn{
			{Key: "name", Label: "Name", Kind: "text"},
			{Key: "driver", Label: "Driver", Kind: "text"},
			{Key: "scope", Label: "Scope", Kind: "text"},
		},
		RowsSource: sdui.DataSource{Endpoint: dockerNetworksRowsEndpoint},
		EmptyState: &sdui.EmptyState{Text: "Docker networks decide which containers can see which. This list should never be empty: Docker keeps bridge, host and none even on a server with nothing running. Empty means an incomplete answer from Docker — check docker.service under Services (systemd)."},
	}
	screen := sdui.Screen{ID: dockerNetworksScreenID, Title: "Networks", Components: []sdui.Component{table}}
	return &sdui.Envelope{Screen: screen}
}

func buildDockerComposeScreen(v sdui.Viewer) *sdui.Envelope {
	table := sdui.TableComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeTable, ID: "compose-table"},
		Columns: []sdui.TableColumn{
			{Key: "stack", Label: "Stack", Kind: "text"},
			{Key: "status", Label: "Status", Kind: "badge", BadgeMap: map[string]string{
				"running": "success", "partial": "warning", "stopped": "neutral", "unknown": "neutral",
			}},
		},
		RowsSource: sdui.DataSource{Endpoint: dockerComposeRowsEndpoint},
		RowActions: []sdui.ActionRef{
			{ActionID: dockerActionComposeUp, Label: "Up", Style: "secondary"},
			{ActionID: dockerActionComposeDown, Label: "Down", Style: "destructive"},
		},
		EmptyState: &sdui.EmptyState{Text: "Each row is a Compose project, grouping the containers that came up together. The list is discovered from container labels, not from files on disk: a docker-compose.yml that never came up does not appear here. Bring the stack up on the host and it joins the list, with the actions your permission allows on the row itself."},
	}
	if !v.IsAdmin() {
		sdui.DropRowActions(&table, func(a sdui.ActionRef) bool { return a.ActionID != dockerActionComposeDown })
	}

	confirm := sdui.ConfirmDestructiveComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeConfirmDestructive, ID: "compose-down-confirm"},
		ActionID:      dockerActionComposeDown,
		Message:       "This stack will be taken down (docker compose down). The containers of its services stop and are removed.",
	}

	screen := sdui.Screen{ID: dockerComposeScreenID, Title: "Compose", Components: []sdui.Component{table}}
	if v.IsAdmin() {
		screen.Components = append(screen.Components, confirm)
	}
	return &sdui.Envelope{Screen: screen}
}

func buildDockerPruneScreenForViewer(v sdui.Viewer) (*sdui.Envelope, error) {
	if !v.IsAdmin() {
		return nil, sdui.ErrScreenNotFound
	}
	return buildDockerPruneScreen(), nil
}

func buildDockerPruneScreen() *sdui.Envelope {
	form := sdui.FormComponent{
		ComponentBase: sdui.ComponentBase{Type: sdui.ComponentTypeForm, ID: "prune-form"},
		Fields: []sdui.FormField{
			{Key: "containers", Label: "Stopped containers", Kind: "bool"},
			{Key: "images", Label: "Unused images", Kind: "bool"},
			{Key: "volumes", Label: "Unused volumes", Kind: "bool"},
			{Key: "build_cache", Label: "Build cache", Kind: "bool"},
		},
		SubmitAction: sdui.ActionRef{ActionID: dockerActionPruneRun, Label: "Clean up", Style: "destructive"},
	}

	confirm := sdui.ConfirmDestructiveComponent{
		ComponentBase:            sdui.ComponentBase{Type: sdui.ComponentTypeConfirmDestructive, ID: "prune-confirm"},
		ActionID:                 dockerActionPruneRun,
		Message:                  "This permanently removes the unused Docker data in the selected categories. It cannot be undone.",
		RequireTypedConfirmation: "PRUNE",
	}

	screen := sdui.Screen{
		ID:         dockerPruneScreenID,
		Title:      "Docker cleanup",
		Components: []sdui.Component{form, confirm},
	}
	return &sdui.Envelope{Screen: screen}
}

func registerDockerContainersRows(api huma.API, deps DockerDeps, mbDeps mobilebff.Deps) {
	registerDockerRows(api, "getDockerContainerRows", "/docker/containers", "Rows of docker.containers", mbDeps.Cfg,
		func(ctx context.Context, _ sdui.Viewer) ([]map[string]any, error) {
			list, err := deps.ListContainers(ctx)
			if err != nil {
				return nil, err
			}
			rows := make([]map[string]any, 0, len(list))
			for _, c := range list {
				rows = append(rows, dockerContainerRow(c))
			}
			return rows, nil
		})
}

func registerDockerImagesRows(api huma.API, deps DockerDeps, mbDeps mobilebff.Deps) {
	registerDockerRows(api, "getDockerImageRows", "/docker/images", "Rows of docker.images", mbDeps.Cfg,
		func(ctx context.Context, _ sdui.Viewer) ([]map[string]any, error) {
			list, err := deps.ListImages(ctx)
			if err != nil {
				return nil, err
			}
			rows := make([]map[string]any, 0, len(list))
			for _, im := range list {
				rows = append(rows, dockerImageRow(im))
			}
			return rows, nil
		})
}

func registerDockerVolumesRows(api huma.API, deps DockerDeps, mbDeps mobilebff.Deps) {
	registerDockerRows(api, "getDockerVolumeRows", "/docker/volumes", "Rows of docker.volumes", mbDeps.Cfg,
		func(ctx context.Context, _ sdui.Viewer) ([]map[string]any, error) {
			list, err := deps.ListVolumes(ctx)
			if err != nil {
				return nil, err
			}
			rows := make([]map[string]any, 0, len(list.Volumes))
			for _, vol := range list.Volumes {
				if vol == nil {
					continue
				}
				rows = append(rows, dockerVolumeRow(*vol))
			}
			return rows, nil
		})
}

func registerDockerNetworksRows(api huma.API, deps DockerDeps, mbDeps mobilebff.Deps) {
	registerDockerRows(api, "getDockerNetworkRows", "/docker/networks", "Rows of docker.networks", mbDeps.Cfg,
		func(ctx context.Context, _ sdui.Viewer) ([]map[string]any, error) {
			list, err := deps.ListNetworks(ctx)
			if err != nil {
				return nil, err
			}
			rows := make([]map[string]any, 0, len(list))
			for _, n := range list {
				rows = append(rows, dockerNetworkRow(n))
			}
			return rows, nil
		})
}

func registerDockerComposeRows(api huma.API, deps DockerDeps, mbDeps mobilebff.Deps) {
	registerDockerRows(api, "getDockerComposeRows", "/docker/compose", "Rows of docker.compose", mbDeps.Cfg,
		func(ctx context.Context, _ sdui.Viewer) ([]map[string]any, error) {
			list, err := deps.ListComposeStacks(ctx)
			if err != nil {
				return nil, err
			}
			rows := make([]map[string]any, 0, len(list))
			for _, p := range list {
				rows = append(rows, dockerComposeRow(p))
			}
			return rows, nil
		})
}

func registerDockerRows(api huma.API, opID, path, summary string, cfg *config.Config, fetch func(context.Context, sdui.Viewer) ([]map[string]any, error)) {
	huma.Register(api, huma.Operation{
		OperationID: opID,
		Method:      http.MethodGet,
		Path:        path,
		Summary:     summary,
		Tags:        []string{"mobile", "sdui", "docker"},
		Middlewares: huma.Middlewares{mobilebff.RequireAuth, serveDockerRows(cfg, fetch)},
	}, dockerRowsDocHandler)
}

type dockerRowsInput struct{}

type dockerRowsOutput struct {
	Body json.RawMessage
}

func dockerRowsDocHandler(_ context.Context, _ *dockerRowsInput) (*dockerRowsOutput, error) {
	return &dockerRowsOutput{Body: json.RawMessage(`{"rows":[]}`)}, nil
}

func serveDockerRows(cfg *config.Config, fetch func(context.Context, sdui.Viewer) ([]map[string]any, error)) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		req, w := humago.Unwrap(ctx)

		username := auth.UserFrom(req)
		v := sdui.ViewerFrom(cfg, username)

		rows, err := fetch(req.Context(), v)
		if err != nil {
			log.Printf("mobilebff/screens: error fetching the docker rows: %v", err)
			httpx.WriteErr(w, http.StatusInternalServerError, "internal_error")
			return
		}
		if rows == nil {
			rows = []map[string]any{}
		}

		body, err := json.Marshal(map[string]any{"rows": rows})
		if err != nil {
			log.Printf("mobilebff/screens: error serializing the docker rows: %v", err)
			httpx.WriteErr(w, http.StatusInternalServerError, "internal_error")
			return
		}

		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}
}

func dockerContainerRow(c types.Container) map[string]any {
	name := c.ID
	if len(c.Names) > 0 {
		name = strings.TrimPrefix(c.Names[0], "/")
	}
	return map[string]any{
		"id":      c.ID,
		"name":    name,
		"image":   c.Image,
		"status":  c.State,
		"created": formatDockerTimestamp(c.Created),
	}
}

func dockerImageRow(im image.Summary) map[string]any {
	repoTag := "(untagged)"
	if len(im.RepoTags) > 0 {
		repoTag = im.RepoTags[0]
	}
	return map[string]any{
		"id":       im.ID,
		"repo_tag": repoTag,
		"size":     formatDockerBytes(im.Size),
		"created":  formatDockerTimestamp(im.Created),
	}
}

func dockerVolumeRow(v volume.Volume) map[string]any {
	size := ""
	if v.UsageData != nil && v.UsageData.Size >= 0 {
		size = formatDockerBytes(v.UsageData.Size)
	}
	return map[string]any{
		"id":     v.Name,
		"name":   v.Name,
		"driver": v.Driver,
		"size":   size,
	}
}

func dockerNetworkRow(n network.Summary) map[string]any {
	return map[string]any{
		"id":     n.ID,
		"name":   n.Name,
		"driver": n.Driver,
		"scope":  n.Scope,
	}
}

func dockerComposeRow(p docksvc.ComposeProject) map[string]any {
	return map[string]any{
		"id":     p.Name,
		"stack":  p.Name,
		"status": p.Status,
	}
}

func formatDockerTimestamp(epoch int64) string {
	if epoch == 0 {
		return ""
	}
	return time.Unix(epoch, 0).UTC().Format(dockerTimestampFormat)
}

func formatDockerBytes(n int64) string {
	if n < 0 {
		return ""
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
