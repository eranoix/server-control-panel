package screens

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/image"

	"server-control-panel/internal/config"
	"server-control-panel/internal/mobilebff/sdui"
)

// fakeContainer/fakeImageSummary build the minimal subset of the real Docker
// SDK structs that dockerContainerRow/dockerImageRow read from — synthetic
// domain values, not a live Docker daemon round trip (that's
// docker_rows_test.go's job).
func fakeContainer(id, name, image, state string, created int64) types.Container {
	return types.Container{
		ID:      id,
		Names:   []string{name},
		Image:   image,
		State:   state,
		Created: created,
	}
}

func fakeImageSummary(id string, repoTags []string, size, created int64) image.Summary {
	return image.Summary{
		ID:       id,
		RepoTags: repoTags,
		Size:     size,
		Created:  created,
	}
}

// testDockerCfg/testDockerViewers mirror testSchedulerCfg/testSchedulerViewers
// (scheduler_test.go) — a real *config.Config through the real
// ViewerFrom/httpx.IsAdmin path, never a Viewer{} literal.
func testDockerCfg() *config.Config {
	return &config.Config{
		SchemaVersion: config.CurrentSchemaVersion,
		Primary:       "docker-admin",
		Users: []config.User{
			{Username: "docker-admin", PasswordHash: "h"},
			{Username: "docker-user", PasswordHash: "h"},
		},
	}
}

func testDockerViewers() (admin, nonAdmin sdui.Viewer) {
	cfg := testDockerCfg()
	return sdui.ViewerFrom(cfg, "docker-admin"), sdui.ViewerFrom(cfg, "docker-user")
}

// --- Test 1: structure ------------------------------------------------

func TestDockerContainersScreen_Structure(t *testing.T) {
	admin, _ := testDockerViewers()
	env := buildDockerContainersScreen(admin)

	table, ok := findComponent(t, env, "containers-table").(sdui.TableComponent)
	if !ok {
		t.Fatalf("containers-table is not a TableComponent")
	}
	gotActions := map[string]bool{}
	for _, ra := range table.RowActions {
		gotActions[ra.ActionID] = true
	}
	for _, want := range []string{dockerActionContainerStart, dockerActionContainerStop, dockerActionContainerRestart, dockerActionContainerRemove} {
		if !gotActions[want] {
			t.Errorf("containers-table.row_actions does not reference %q: %v", want, table.RowActions)
		}
	}

	confirm, ok := findComponent(t, env, "container-remove-confirm").(sdui.ConfirmDestructiveComponent)
	if !ok {
		t.Fatalf("container-remove-confirm is not a ConfirmDestructiveComponent")
	}
	if confirm.ActionID != dockerActionContainerRemove {
		t.Errorf("container-remove-confirm.action_id = %q, want %q", confirm.ActionID, dockerActionContainerRemove)
	}
}

func TestDockerImagesScreen_Structure(t *testing.T) {
	admin, _ := testDockerViewers()
	env := buildDockerImagesScreen(admin)

	table, ok := findComponent(t, env, "images-table").(sdui.TableComponent)
	if !ok {
		t.Fatalf("images-table is not a TableComponent")
	}
	if len(table.RowActions) != 1 || table.RowActions[0].ActionID != dockerActionImageRemove {
		t.Errorf("images-table.row_actions = %v, want only %q", table.RowActions, dockerActionImageRemove)
	}

	confirm, ok := findComponent(t, env, "image-remove-confirm").(sdui.ConfirmDestructiveComponent)
	if !ok {
		t.Fatalf("image-remove-confirm is not a ConfirmDestructiveComponent")
	}
	if confirm.ActionID != dockerActionImageRemove {
		t.Errorf("image-remove-confirm.action_id = %q, want %q", confirm.ActionID, dockerActionImageRemove)
	}
}

// TestDockerVolumesNetworksScreens_ListOnlyForEveryViewer proves volumes and
// networks never carry a row action or a confirm_destructive component, for
// ANY viewer — internal/docker exposes no single-item delete for either
// (see deps.go's DockerDeps doc comment).
func TestDockerVolumesNetworksScreens_ListOnlyForEveryViewer(t *testing.T) {
	admin, nonAdmin := testDockerViewers()
	for name, v := range map[string]sdui.Viewer{"admin": admin, "nonadmin": nonAdmin} {
		volEnv := buildDockerVolumesScreen()
		netEnv := buildDockerNetworksScreen()
		_ = v // buildDockerVolumesScreen/buildDockerNetworksScreen take no Viewer — identical for both, proven below.

		volTable, ok := findComponent(t, volEnv, "volumes-table").(sdui.TableComponent)
		if !ok {
			t.Fatalf("%s: volumes-table is not a TableComponent", name)
		}
		if len(volTable.RowActions) != 0 {
			t.Errorf("%s: volumes-table.row_actions = %v, want empty", name, volTable.RowActions)
		}
		for _, c := range volEnv.Screen.Components {
			if c.ComponentType() == sdui.ComponentTypeConfirmDestructive {
				t.Errorf("%s: docker.volumes contains a ConfirmDestructiveComponent (%s) — there should not be any", name, c.Base().ID)
			}
		}

		netTable, ok := findComponent(t, netEnv, "networks-table").(sdui.TableComponent)
		if !ok {
			t.Fatalf("%s: networks-table is not a TableComponent", name)
		}
		if len(netTable.RowActions) != 0 {
			t.Errorf("%s: networks-table.row_actions = %v, want empty", name, netTable.RowActions)
		}
		for _, c := range netEnv.Screen.Components {
			if c.ComponentType() == sdui.ComponentTypeConfirmDestructive {
				t.Errorf("%s: docker.networks contains a ConfirmDestructiveComponent (%s) — there should not be any", name, c.Base().ID)
			}
		}
	}
}

func TestDockerComposeScreen_Structure(t *testing.T) {
	admin, _ := testDockerViewers()
	env := buildDockerComposeScreen(admin)

	table, ok := findComponent(t, env, "compose-table").(sdui.TableComponent)
	if !ok {
		t.Fatalf("compose-table is not a TableComponent")
	}
	gotActions := map[string]bool{}
	for _, ra := range table.RowActions {
		gotActions[ra.ActionID] = true
	}
	for _, want := range []string{dockerActionComposeUp, dockerActionComposeDown} {
		if !gotActions[want] {
			t.Errorf("compose-table.row_actions does not reference %q: %v", want, table.RowActions)
		}
	}

	confirm, ok := findComponent(t, env, "compose-down-confirm").(sdui.ConfirmDestructiveComponent)
	if !ok {
		t.Fatalf("compose-down-confirm is not a ConfirmDestructiveComponent")
	}
	if confirm.ActionID != dockerActionComposeDown {
		t.Errorf("compose-down-confirm.action_id = %q, want %q", confirm.ActionID, dockerActionComposeDown)
	}
}

// TestDockerPruneScreen_Structure proves the prune screen has exactly one
// form and one confirm_destructive, no table — and that RequireTypedConfirmation
// is set (unlike scheduler.job.delete), since prune is genuinely irreversible.
func TestDockerPruneScreen_Structure(t *testing.T) {
	env := buildDockerPruneScreen()

	counts := map[sdui.ComponentType]int{}
	for _, c := range env.Screen.Components {
		counts[c.ComponentType()]++
	}
	want := map[sdui.ComponentType]int{
		sdui.ComponentTypeForm:               1,
		sdui.ComponentTypeConfirmDestructive: 1,
	}
	for ct, n := range want {
		if counts[ct] != n {
			t.Errorf("components of type %q = %d, want %d (full count: %v)", ct, counts[ct], n, counts)
		}
	}
	if counts[sdui.ComponentTypeTable] != 0 {
		t.Errorf("docker.prune should not have any table: %v", counts)
	}

	confirm, ok := findComponent(t, env, "prune-confirm").(sdui.ConfirmDestructiveComponent)
	if !ok {
		t.Fatalf("prune-confirm is not a ConfirmDestructiveComponent")
	}
	if confirm.ActionID != dockerActionPruneRun {
		t.Errorf("prune-confirm.action_id = %q, want %q", confirm.ActionID, dockerActionPruneRun)
	}
	if confirm.RequireTypedConfirmation == "" {
		t.Errorf("prune-confirm.require_typed_confirmation is empty — prune is destructive and irreversible, it should require typed text")
	}
}

// TestDockerPruneScreen_AdminOnly proves a non-admin Build call returns
// ErrScreenNotFound (404-never-403), never an emptied-but-present envelope.
func TestDockerPruneScreen_AdminOnly(t *testing.T) {
	admin, nonAdmin := testDockerViewers()

	if _, err := buildDockerPruneScreenForViewer(admin); err != nil {
		t.Fatalf("admin: buildDockerPruneScreenForViewer: %v", err)
	}
	env, err := buildDockerPruneScreenForViewer(nonAdmin)
	if err != sdui.ErrScreenNotFound {
		t.Fatalf("non-admin: err = %v, want sdui.ErrScreenNotFound", err)
	}
	if env != nil {
		t.Fatalf("non-admin: envelope is not nil: %v", env)
	}
}

// --- Test 2 (RBAC omission, on bytes) + Test 3 (non-vacuity) -----------

// TestDockerContainersScreen_RBACOmissionOnBytes proves the admin envelope
// contains the remove action id and non-admin's does not — while both
// envelopes carry the identical table/columns (no per-row scoping exists in
// internal/docker beyond admin/non-admin, per PLAN.md Risks).
func TestDockerContainersScreen_RBACOmissionOnBytes(t *testing.T) {
	admin, nonAdmin := testDockerViewers()

	adminBytes, err := json.Marshal(buildDockerContainersScreen(admin))
	if err != nil {
		t.Fatalf("marshal admin: %v", err)
	}
	nonAdminBytes, err := json.Marshal(buildDockerContainersScreen(nonAdmin))
	if err != nil {
		t.Fatalf("marshal non-admin: %v", err)
	}

	if !strings.Contains(string(adminBytes), dockerActionContainerRemove) {
		t.Errorf("admin envelope does not contain %q: %s", dockerActionContainerRemove, adminBytes)
	}
	if strings.Contains(string(nonAdminBytes), dockerActionContainerRemove) {
		t.Errorf("non-admin envelope contains %q: %s", dockerActionContainerRemove, nonAdminBytes)
	}
	// Non-vacuity: the remaining row_actions stay present for the non-admin.
	for _, want := range []string{dockerActionContainerStart, dockerActionContainerStop, dockerActionContainerRestart} {
		if !strings.Contains(string(nonAdminBytes), want) {
			t.Errorf("non-admin envelope does not contain %q (non-destructive action, should stay visible): %s", want, nonAdminBytes)
		}
	}
}

func TestDockerImagesScreen_RBACOmissionOnBytes(t *testing.T) {
	admin, nonAdmin := testDockerViewers()

	adminBytes, _ := json.Marshal(buildDockerImagesScreen(admin))
	nonAdminBytes, _ := json.Marshal(buildDockerImagesScreen(nonAdmin))

	if !strings.Contains(string(adminBytes), dockerActionImageRemove) {
		t.Errorf("admin envelope does not contain %q: %s", dockerActionImageRemove, adminBytes)
	}
	if strings.Contains(string(nonAdminBytes), dockerActionImageRemove) {
		t.Errorf("non-admin envelope contains %q: %s", dockerActionImageRemove, nonAdminBytes)
	}
}

func TestDockerComposeScreen_RBACOmissionOnBytes(t *testing.T) {
	admin, nonAdmin := testDockerViewers()

	adminBytes, _ := json.Marshal(buildDockerComposeScreen(admin))
	nonAdminBytes, _ := json.Marshal(buildDockerComposeScreen(nonAdmin))

	if !strings.Contains(string(adminBytes), dockerActionComposeDown) {
		t.Errorf("admin envelope does not contain %q: %s", dockerActionComposeDown, adminBytes)
	}
	if strings.Contains(string(nonAdminBytes), dockerActionComposeDown) {
		t.Errorf("non-admin envelope contains %q: %s", dockerActionComposeDown, nonAdminBytes)
	}
	if !strings.Contains(string(nonAdminBytes), dockerActionComposeUp) {
		t.Errorf("non-admin envelope does not contain %q (non-destructive action, should stay visible): %s", dockerActionComposeUp, nonAdminBytes)
	}
}

// --- Test 4: no client-side logic --------------------------------------

func TestDockerScreens_NoClientSideLogicKeys(t *testing.T) {
	admin, _ := testDockerViewers()
	envs := map[string]*sdui.Envelope{
		"containers": buildDockerContainersScreen(admin),
		"images":     buildDockerImagesScreen(admin),
		"volumes":    buildDockerVolumesScreen(),
		"networks":   buildDockerNetworksScreen(),
		"compose":    buildDockerComposeScreen(admin),
		"prune":      buildDockerPruneScreen(),
	}
	forbidden := []string{"\"condition\"", "\"visible_when\"", "\"expression\""}
	for name, env := range envs {
		body, err := json.Marshal(env)
		if err != nil {
			t.Fatalf("%s: marshal: %v", name, err)
		}
		for _, key := range forbidden {
			if strings.Contains(string(body), key) {
				t.Errorf("%s: payload contains a forbidden client-side-logic key %s: %s", name, key, body)
			}
		}
		idx := 0
		for {
			i := strings.Index(string(body)[idx:], "\"permission")
			if i < 0 {
				break
			}
			i += idx
			if !strings.HasPrefix(string(body)[i:], "\"permission_hint\"") {
				end := i + 40
				if end > len(body) {
					end = len(body)
				}
				t.Errorf("%s: payload contains a \"permission...\" key that is not permission_hint, around: %s", name, string(body)[i:end])
			}
			idx = i + len("\"permission")
		}
	}
}

// --- Test 5: preformatted values ----------------------------------------

func TestFormatDockerTimestamp_ZeroIsEmpty(t *testing.T) {
	if got := formatDockerTimestamp(0); got != "" {
		t.Errorf("formatDockerTimestamp(0) = %q, want \"\"", got)
	}
}

func TestFormatDockerBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{-1, ""},
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KiB"},
		{1536, "1.5 KiB"},
		{1024 * 1024, "1.0 MiB"},
	}
	for _, c := range cases {
		if got := formatDockerBytes(c.in); got != c.want {
			t.Errorf("formatDockerBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestDockerRowShapingFuncs_NeverEmitRawNumbers proves the row-shaping
// functions always produce display-ready strings for size/timestamp keys,
// never a raw number — using synthetic domain values, not a live Docker
// daemon (that round trip is covered end-to-end by docker_rows_test.go).
func TestDockerRowShapingFuncs_NeverEmitRawNumbers(t *testing.T) {
	row := dockerContainerRow(fakeContainer("c1", "/web", "nginx:latest", "running", 1798000000))
	if _, isString := row["created"].(string); !isString {
		t.Errorf("container row \"created\" = %v (%T), want string", row["created"], row["created"])
	}

	imgRow := dockerImageRow(fakeImageSummary("img1", []string{"nginx:latest"}, 1048576, 1798000000))
	if _, isString := imgRow["size"].(string); !isString {
		t.Errorf("image row \"size\" = %v (%T), want string", imgRow["size"], imgRow["size"])
	}
	if _, isString := imgRow["created"].(string); !isString {
		t.Errorf("image row \"created\" = %v (%T), want string", imgRow["created"], imgRow["created"])
	}
}
