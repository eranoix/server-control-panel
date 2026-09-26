package screens

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	dockertypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"

	"server-control-panel/internal/auth"
	docksvc "server-control-panel/internal/docker"
	"server-control-panel/internal/mobilebff"
)

// fakeDockerRowsDeps builds a DockerDeps with only the List* fields
// populated — the rows endpoints never call any mutation closure, so
// start/stop/remove/etc. are deliberately left nil (a nil call would panic,
// which is exactly what we want if a rows handler ever starts calling one).
func fakeDockerRowsDeps() DockerDeps {
	return DockerDeps{
		ListContainers: func(context.Context) ([]dockertypes.Container, error) {
			return []dockertypes.Container{
				{ID: "c1", Names: []string{"/web"}, Image: "nginx:latest", State: "running", Created: 1798000000},
			}, nil
		},
		ListImages: func(context.Context) ([]image.Summary, error) {
			return []image.Summary{
				{ID: "sha256:abc", RepoTags: []string{"nginx:latest"}, Size: 1048576, Created: 1798000000},
			}, nil
		},
		ListVolumes: func(context.Context) (volume.ListResponse, error) {
			return volume.ListResponse{
				Volumes: []*volume.Volume{
					{Name: "vol1", Driver: "local", UsageData: &volume.UsageData{Size: 2048, RefCount: 1}},
				},
			}, nil
		},
		ListNetworks: func(context.Context) ([]network.Summary, error) {
			return []network.Summary{
				{ID: "net1", Name: "bridge", Driver: "bridge", Scope: "local"},
			}, nil
		},
		ListComposeStacks: func(context.Context) ([]docksvc.ComposeProject, error) {
			return []docksvc.ComposeProject{
				{Name: "myapp", WorkingDir: "/opt/stacks/myapp", Status: "running"},
			}, nil
		},
	}
}

// newDockerRowsMux wires only the five registerDockerXRows functions onto a
// throwaway huma.API — mirrors newSchedulerRowsMux's rationale exactly:
// screens.RegisterDocker itself goes through the process-global sdui.Register
// / sdui.RegisterAction registries (which panic on double-registration), so
// tests exercise the rows plumbing directly instead of through RegisterDocker.
func newDockerRowsMux(deps DockerDeps) *http.ServeMux {
	mux := http.NewServeMux()
	api := humago.NewWithPrefix(mux, mobilebff.Prefix, huma.DefaultConfig("docker-rows-test", "0"))
	mbDeps := mobilebff.Deps{Cfg: testDockerCfg()}
	registerDockerContainersRows(api, deps, mbDeps)
	registerDockerImagesRows(api, deps, mbDeps)
	registerDockerVolumesRows(api, deps, mbDeps)
	registerDockerNetworksRows(api, deps, mbDeps)
	registerDockerComposeRows(api, deps, mbDeps)
	return mux
}

type dockerRowsBody struct {
	Rows []map[string]any `json:"rows"`
}

func doDockerRowsRequest(t *testing.T, mux *http.ServeMux, path, username string) (*httptest.ResponseRecorder, dockerRowsBody) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, mobilebff.Prefix+path, nil)
	if username != "" {
		req = req.WithContext(auth.WithUser(req.Context(), username))
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var body dockerRowsBody
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: decode: %v (body=%s)", path, err, rec.Body.String())
		}
	}
	return rec, body
}

func TestDockerRows_Unauthenticated(t *testing.T) {
	mux := newDockerRowsMux(fakeDockerRowsDeps())
	for _, path := range []string{"/docker/containers", "/docker/images", "/docker/volumes", "/docker/networks", "/docker/compose"} {
		rec, _ := doDockerRowsRequest(t, mux, path, "")
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want 401 (body=%s)", path, rec.Code, rec.Body.String())
		}
	}
}

// TestDockerContainersRows_WireShape pins {"rows":[{"id","name","image","status","created"}]}.
func TestDockerContainersRows_WireShape(t *testing.T) {
	mux := newDockerRowsMux(fakeDockerRowsDeps())
	rec, body := doDockerRowsRequest(t, mux, "/docker/containers", "docker-admin")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if len(body.Rows) != 1 {
		t.Fatalf("rows = %v, want 1 row", body.Rows)
	}
	row := body.Rows[0]
	wantKeys := []string{"id", "name", "image", "status", "created"}
	for _, k := range wantKeys {
		if _, ok := row[k]; !ok {
			t.Errorf("row does not have the %q key: %v", k, row)
		}
	}
	if row["name"] != "web" {
		t.Errorf("name = %v, want \"web\" (leading / stripped)", row["name"])
	}
	if row["status"] != "running" {
		t.Errorf("status = %v, want \"running\"", row["status"])
	}
	if _, isString := row["created"].(string); !isString {
		t.Errorf("created = %v (%T), want a pre-formatted string", row["created"], row["created"])
	}
}

// TestDockerImagesRows_WireShape pins {"rows":[{"id","repo_tag","size","created"}]}.
func TestDockerImagesRows_WireShape(t *testing.T) {
	mux := newDockerRowsMux(fakeDockerRowsDeps())
	rec, body := doDockerRowsRequest(t, mux, "/docker/images", "docker-admin")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if len(body.Rows) != 1 {
		t.Fatalf("rows = %v, want 1 row", body.Rows)
	}
	row := body.Rows[0]
	wantKeys := []string{"id", "repo_tag", "size", "created"}
	for _, k := range wantKeys {
		if _, ok := row[k]; !ok {
			t.Errorf("row does not have the %q key: %v", k, row)
		}
	}
	if row["repo_tag"] != "nginx:latest" {
		t.Errorf("repo_tag = %v, want \"nginx:latest\"", row["repo_tag"])
	}
	if _, isString := row["size"].(string); !isString {
		t.Errorf("size = %v (%T), want a pre-formatted string (never a raw number)", row["size"], row["size"])
	}
}

// TestDockerVolumesRows_WireShape pins {"rows":[{"id","name","driver","size"}]}
// and proves the interface{} unwrap in internal/api/api.go's ListVolumes
// closure (asserting volume.ListResponse) round-trips correctly end to end
// through this seam.
func TestDockerVolumesRows_WireShape(t *testing.T) {
	mux := newDockerRowsMux(fakeDockerRowsDeps())
	rec, body := doDockerRowsRequest(t, mux, "/docker/volumes", "docker-admin")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if len(body.Rows) != 1 {
		t.Fatalf("rows = %v, want 1 row", body.Rows)
	}
	row := body.Rows[0]
	wantKeys := []string{"id", "name", "driver", "size"}
	for _, k := range wantKeys {
		if _, ok := row[k]; !ok {
			t.Errorf("row does not have the %q key: %v", k, row)
		}
	}
	if row["name"] != "vol1" || row["driver"] != "local" {
		t.Errorf("row = %v, want name=vol1 driver=local", row)
	}
	if _, isString := row["size"].(string); !isString {
		t.Errorf("size = %v (%T), want a pre-formatted string", row["size"], row["size"])
	}
}

// TestDockerVolumesRows_NilUsageDataSizeIsEmptyString proves a volume with no
// UsageData (the plain list call docker.Client.Volumes makes never populates
// it) renders "" for size, never a panic or "-1".
func TestDockerVolumesRows_NilUsageDataSizeIsEmptyString(t *testing.T) {
	deps := fakeDockerRowsDeps()
	deps.ListVolumes = func(context.Context) (volume.ListResponse, error) {
		return volume.ListResponse{
			Volumes: []*volume.Volume{{Name: "vol-no-usage", Driver: "local"}},
		}, nil
	}
	mux := newDockerRowsMux(deps)
	_, body := doDockerRowsRequest(t, mux, "/docker/volumes", "docker-admin")
	if len(body.Rows) != 1 {
		t.Fatalf("rows = %v, want 1 row", body.Rows)
	}
	if body.Rows[0]["size"] != "" {
		t.Errorf("size = %v, want \"\" (no UsageData)", body.Rows[0]["size"])
	}
}

// TestDockerNetworksRows_WireShape pins {"rows":[{"id","name","driver","scope"}]}.
func TestDockerNetworksRows_WireShape(t *testing.T) {
	mux := newDockerRowsMux(fakeDockerRowsDeps())
	rec, body := doDockerRowsRequest(t, mux, "/docker/networks", "docker-admin")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if len(body.Rows) != 1 {
		t.Fatalf("rows = %v, want 1 row", body.Rows)
	}
	row := body.Rows[0]
	wantKeys := []string{"id", "name", "driver", "scope"}
	for _, k := range wantKeys {
		if _, ok := row[k]; !ok {
			t.Errorf("row does not have the %q key: %v", k, row)
		}
	}
	if row["name"] != "bridge" || row["scope"] != "local" {
		t.Errorf("row = %v, want name=bridge scope=local", row)
	}
}

// TestDockerComposeRows_WireShape pins {"rows":[{"id","stack","status"}]} —
// "id" and "stack" both equal the project name (compose has no separate id).
func TestDockerComposeRows_WireShape(t *testing.T) {
	mux := newDockerRowsMux(fakeDockerRowsDeps())
	rec, body := doDockerRowsRequest(t, mux, "/docker/compose", "docker-admin")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if len(body.Rows) != 1 {
		t.Fatalf("rows = %v, want 1 row", body.Rows)
	}
	row := body.Rows[0]
	if row["id"] != "myapp" || row["stack"] != "myapp" || row["status"] != "running" {
		t.Errorf("row = %v, want id=stack=myapp status=running", row)
	}
	// working_dir must NEVER be present on the wire — it is a server-only
	// resolution detail, never client-supplied or client-visible.
	if _, ok := row["working_dir"]; ok {
		t.Errorf("row exposes working_dir on the wire, which should never happen: %v", row)
	}
}

// TestDockerRows_SameShapeForAdminAndNonAdmin proves containers/images/
// volumes/networks/compose rows are identical for admin and non-admin —
// internal/docker applies no per-viewer scoping to any of these lists
// (verified against handlers_docker.go), so the rows endpoints must not
// invent scoping that does not exist upstream.
func TestDockerRows_SameShapeForAdminAndNonAdmin(t *testing.T) {
	mux := newDockerRowsMux(fakeDockerRowsDeps())
	for _, path := range []string{"/docker/containers", "/docker/images", "/docker/volumes", "/docker/networks", "/docker/compose"} {
		_, adminBody := doDockerRowsRequest(t, mux, path, "docker-admin")
		_, userBody := doDockerRowsRequest(t, mux, path, "docker-user")
		adminJSON, _ := json.Marshal(adminBody)
		userJSON, _ := json.Marshal(userBody)
		if string(adminJSON) != string(userJSON) {
			t.Errorf("%s: admin and non-admin diverge: admin=%s non-admin=%s", path, adminJSON, userJSON)
		}
	}
}
