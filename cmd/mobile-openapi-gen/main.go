// Command mobile-openapi-gen generates android/data/mobile-api-client/openapi/mobile-v1.yaml
// from the typed route registry in internal/mobilebff — never the other way
// round. Run through `make mobile-openapi-spec`; it is not part of the binary
// served in production.
//
// huma.DefaultConfig produces OpenAPI 3.1.0 by default. openapi-generator's
// Kotlin generator has historically incomplete 3.1 support, so this command
// already writes the document converted to 3.0.3 via (*huma.OpenAPI).DowngradeYAML —
// which avoids discovering the incompatibility only at Gradle build time.
package main

import (
	"log"
	"net/http"
	"os"
	"path/filepath"

	"server-control-panel/internal/mobilebff"
	"server-control-panel/internal/mobilebff/screens"
)

const outputPath = "android/data/mobile-api-client/openapi/mobile-v1.yaml"

func main() {
	// scheduler.jobs is the first screen whose HTTP routes (the rows_source of
	// jobs-table) only exist after an explicit Register(deps) — no init()
	// registers them, because in production internal/api/api.go is the only place
	// holding a real SchedulerDeps. Without this call, GET /scheduler/jobs would
	// never enter the spec: this binary never imports internal/api.
	// screens.SchedulerDeps{} (the zero value) is safe here for the same reason
	// mobilebff.Deps{} below is safe — no field is read until a handler really
	// runs, and this command never invokes a handler, it only introspects the
	// shape of the types.
	screens.Register(screens.SchedulerDeps{})

	// docker.containers/images/volumes/networks/compose register their five row
	// routes (rows_source) the same way: only through an explicit
	// RegisterDocker(deps), never through an init(). screens.DockerDeps{} (the
	// zero value) is safe for the same reason as SchedulerDeps{} above —
	// RegisterDocker only assembles the shape of the routes (huma.Register) and
	// each screen's Envelope; no field is called until a handler really runs,
	// which this command never does.
	screens.RegisterDocker(screens.DockerDeps{})

	// system.history/processes/ports/systemd/metrics register their seven
	// row/series routes (rows_source/series_source) the same way: only through an
	// explicit RegisterSystem(deps), never through an init().
	// screens.SystemDeps{} (the zero value) is safe for the same reason as
	// DockerDeps{} above — RegisterSystem only assembles the shape of the routes
	// (huma.Register) and each screen's Envelope; no field is called until a
	// handler really runs, which this command never does. Without this call,
	// GET /system/metrics/cpu (and the other six routes) would never enter the
	// spec — exactly the bug the Docker screens hit and fixed for
	// RegisterDocker.
	screens.RegisterSystem(screens.SystemDeps{})

	// security.users/secrets/sessions/audit and security.ufw/adguard/devices/
	// economia register their eight row/detail routes (rows_source/data_source)
	// the same way: only through an explicit RegisterSecurity(deps)/
	// RegisterNetwork(deps), never through an init(). screens.SecurityDeps{}/
	// screens.NetworkDeps{} (the zero values) are safe for the same reason as
	// SystemDeps{} above — each Register only assembles the shape of the routes
	// (huma.Register) and each screen's Envelope; no field is called until a
	// handler really runs, which this command never does. Without these two
	// calls, GET /security/users (and the other seven routes) would never enter
	// the spec — the same class of bug already found and fixed for
	// RegisterDocker/RegisterSystem.
	screens.RegisterSecurity(screens.SecurityDeps{})
	screens.RegisterNetwork(screens.NetworkDeps{})

	// alerts.rules registers its row route (rows_source, GET /alerts/rules) the
	// same way: only through an explicit RegisterAlerts(deps), never through an
	// init(). screens.AlertsDeps{} (the zero value) is safe for the same reason
	// as NetworkDeps{} above — RegisterAlerts only assembles the shape of the
	// route (huma.Register) and the screen's Envelope; no field is called until
	// a handler really runs, which this command never does. Without this call,
	// GET /alerts/rules would never enter the spec — the same class of bug
	// already found and fixed for RegisterDocker/RegisterSystem.
	screens.RegisterAlerts(screens.AlertsDeps{})

	// ai.settings/jira.issues/deploy.apps/queue.jobs register their
	// row/detail/action routes (rows_source/data_source/actions) the same way:
	// only through an explicit RegisterMisc(deps), never through an init().
	// screens.MiscDeps{} (the zero value) is safe for the same reason as
	// AlertsDeps{} above — RegisterMisc only assembles the shape of the routes
	// (huma.Register) and each screen's Envelope; no field is called until a
	// handler really runs, which this command never does. Without this call,
	// GET /misc/deploy/apps (and the remaining routes of the four screens) would
	// never enter the spec — the same class of bug already found and fixed for
	// RegisterDocker/RegisterSystem. These four screens manage the PaaS catalogue
	// in internal/deploy and the generic queue in internal/queue — never the
	// self-deploy trigger (POST /ops/deploy) nor its status (GET /ops/status), a
	// sibling and non-overlapping mechanism.
	screens.RegisterMisc(screens.MiscDeps{})

	// The mobile BFF exposes TWO separate huma.API surfaces — the authenticated
	// one (mobilebff.Mount, under auth.Middleware in production) and the public
	// one (mobilebff.MountPublic, the four passkey routes that have to happen
	// before a session exists; see internal/mobilebff/registry_public.go). Each
	// Mount/MountPublic builds its OWN huma.Config/OpenAPI internally, so the two
	// specs are born independent — the generated Kotlin client, however, has to
	// see both surfaces as a single document (the app does not tell a public
	// route from a protected one by base path). That is why we merge Paths and
	// the Components.Schemas registry below instead of emitting two files.
	protectedMux := http.NewServeMux()
	protectedAPI := mobilebff.Mount(protectedMux, mobilebff.Deps{})

	publicMux := http.NewServeMux()
	publicAPI := mobilebff.MountPublic(publicMux, mobilebff.Deps{})

	spec := protectedAPI.OpenAPI()
	publicSpec := publicAPI.OpenAPI()

	for path, item := range publicSpec.Paths {
		if _, dup := spec.Paths[path]; dup {
			log.Fatalf("mobile-openapi-gen: duplicate path between public and authenticated routes: %s", path)
		}
		spec.Paths[path] = item
	}
	if publicSpec.Components != nil && publicSpec.Components.Schemas != nil &&
		spec.Components != nil && spec.Components.Schemas != nil {
		dst := spec.Components.Schemas.Map()
		for name, schema := range publicSpec.Components.Schemas.Map() {
			// Names like "ErrorModel"/"ErrorDetail" are injected by huma itself into
			// every huma.API built through DefaultConfig — the two trees (authenticated
			// and public) generate the SAME content for them deterministically, so the
			// duplicate is expected and harmless: the version already present stays,
			// nothing is overwritten. A duplicated type name of OURS (not injected by
			// huma) would be a genuine bug of names colliding between the two route
			// packages — but since both are born from the same mobilebff package with
			// unique Go type names, that should not happen; if it does, it is silently
			// resolved in favour of the authenticated version, the same policy huma
			// applies to anonymous types that collide by hint.
			if _, dup := dst[name]; dup {
				continue
			}
			dst[name] = schema
		}
	}

	yamlBytes, err := spec.DowngradeYAML()
	if err != nil {
		log.Fatalf("mobile-openapi-gen: downgrade to OpenAPI 3.0.3 failed: %v", err)
	}

	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		log.Fatalf("mobile-openapi-gen: creating the output directory: %v", err)
	}

	if err := os.WriteFile(outputPath, yamlBytes, 0o644); err != nil {
		log.Fatalf("mobile-openapi-gen: writing %s: %v", outputPath, err)
	}

	log.Printf("mobile-openapi-gen: %s generated (%d bytes)", outputPath, len(yamlBytes))
}
