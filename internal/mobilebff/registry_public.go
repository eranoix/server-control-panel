package mobilebff

// registry_public.go — a mechanism PARALLEL to registry.go, for the handful of
// mobile BFF routes that have to sit OUTSIDE auth.Middleware.
//
// internal/api/api.go mounts the ENTIRE mobilebff.Mount surface under a
// "protected" mux, which is in turn wrapped by r.auth.Middleware (applied with
// no path exception to "/api/" and "/ws/") — any request without a valid
// vpsm_token/Bearer session gets a 401 right there, before it ever reaches a
// handler. That is correct for every already-authenticated route, but it is
// incompatible with login and with the passkey registration step: by
// definition, those calls happen BEFORE any session exists (login/begin,
// login/finish) or are authorized by a single-purpose ceremony token rather
// than an ordinary session (register/begin, register/finish — see
// internal/auth/tokens.go, IssueWebAuthnRegToken).
//
// The way out, instead of reopening auth.Middleware to accept those cases
// (which would weaken the "every /api/ requires a session" guarantee for EVERY
// route, not just the four passkey ones), is to mount a SEPARATE huma.API
// directly on the top-level (unprotected) mux — the same pattern
// /api/videocall/join-by-pin, /api/agent/hook and /.well-known/assetlinks.json
// already use in internal/api/api.go to escape the wrap. It works without any
// route ambiguity because humago registers each operation as an EXACT
// "METHOD /full/path" pattern (see adapters/humago), and the stdlib ServeMux
// (Go 1.22+) always prefers the more specific pattern over a subtree pattern
// like "/api/" — there is no conflict, and no registration order that matters,
// between this mux and the "protected" one.
//
// Routes registered here must NEVER depend on auth.UserFrom/UserFromContext
// (there is no preceding Middleware to have filled them in) — each public
// handler is responsible for its OWN authorization check (ceremony token,
// WebAuthn credential, etc.), never for trusting request context.

import (
	"net/http"
	"sort"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
)

type publicNamedRegistrar struct {
	name string
	fn   Registrar
}

var publicRegistrars []publicNamedRegistrar

// RegisterPublic signs a group of PUBLIC routes (outside auth.Middleware) up
// with the mobile BFF. Same rules as Register: a unique, stable name, called
// from an init() in the handlers_*.go file itself.
func RegisterPublic(name string, fn Registrar) {
	for _, r := range publicRegistrars {
		if r.name == name {
			panic("mobilebff: duplicate public registration: " + name)
		}
	}
	publicRegistrars = append(publicRegistrars, publicNamedRegistrar{name: name, fn: fn})
}

// MountPublic registers the mobile BFF's public routes on the given mux.
// `mux` MUST be the top-level mux (the same one
// /.well-known/assetlinks.json and /api/videocall/join-by-pin are registered
// on), NEVER the "protected" mux — see this file's header comment. It uses the
// same Prefix ("/api/mobile/v1") as Mount: the generated Kotlin client does not
// tell a public route from a protected one by base path, only by the presence
// or absence of the session Bearer/cookie it already sends by default.
func MountPublic(mux *http.ServeMux, deps Deps) huma.API {
	config := huma.DefaultConfig("vps-manager mobile BFF (public)", "1.0.0")
	// DELIBERATE OMISSION: unlike Mount, applyBearerSecurity (security.go) is
	// NOT called here. These routes happen before any session exists, so
	// declaring `security` on them would be a spec that lies — and it would make
	// the generated Kotlin client try to attach a Bearer that does not exist,
	// precisely on /auth/login and /auth/refresh. If a public route ever starts
	// accepting a credential, mark that operation individually, never the whole
	// mount.
	api := humago.NewWithPrefix(mux, Prefix, config)

	sorted := make([]publicNamedRegistrar, len(publicRegistrars))
	copy(sorted, publicRegistrars)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].name < sorted[j].name })
	for _, r := range sorted {
		r.fn(api, deps)
	}

	return api
}
