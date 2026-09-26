// Package mobilebff is the backend-for-frontend of the native Android app —
// the ONLY HTTP surface the app is allowed to call. It imports existing domain
// packages (auth, config, and whatever else each endpoint needs); never the
// other way around — no domain package should ever import mobilebff.
//
// Why this package exists (instead of loose handlers in internal/api): the
// previous mobile attempt built a second API surface in parallel with the
// SPA's, and the two diverged silently — a field changed in one handler did not
// show up in the other, and the app broke without a single test noticing,
// because there was no single contract to derive both sides from. Here the
// contract is generated, not hand-maintained: every route is registered through
// `huma.Register` with concrete Go input/output types, and the OpenAPI document
// (mobile-v1.yaml, produced by `make mobile-openapi-spec`) is derived FROM that
// registry — never written by hand. The app's Kotlin client is in turn generated
// from that same YAML (org.openapi.generator, see
// android/data/mobile-api-client). As long as that pipeline is respected, a
// field mismatch between server and app becomes a Kotlin compile error, not a
// silent bug discovered in production.
package mobilebff

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
)

// Prefix is the base path of every mobile BFF route. The Android app and the
// generated Kotlin client must assume no other prefix — changing this is a
// contract change that requires regenerating the client.
const Prefix = "/api/mobile/v1"

// Mount registers the mobile BFF on the given mux and returns the resulting
// huma.API (handy for anyone who needs to read `.OpenAPI()`, such as the spec
// generator in cmd/mobile-openapi-gen). `mux` is the stdlib's plain
// *http.ServeMux — the same type used everywhere else in the project (see
// internal/api/api.go); the humago adapter demands no third-party router.
//
// `mux` must be the protected mux (the same auth tier as every authenticated
// route, see internal/api/api.go) — this package exposes no unauthenticated
// mux of its own. Fields of `deps` may be nil (as used by
// cmd/mobile-openapi-gen, which only needs the shape of the types to generate
// the spec and never invokes a real handler).
//
// The routes are not listed here: each handlers_*.go file signs itself up via
// Register() in an init(). See registry.go for why.
func Mount(mux *http.ServeMux, deps Deps) huma.API {
	config := huma.DefaultConfig("vps-manager mobile BFF", "1.0.0")
	// Declares bearer JWT in the OpenAPI document and marks EVERY route
	// registered here as protected — that is what makes the generated Kotlin
	// client attach the Authorization header instead of being born with dead
	// hooks. Purely descriptive: what demands the token at runtime is
	// auth.Middleware, not huma. See security.go. MountPublic does NOT call
	// this, on purpose.
	applyBearerSecurity(&config)
	api := humago.NewWithPrefix(mux, Prefix, config)

	runRegistrars(api, deps)

	return api
}
