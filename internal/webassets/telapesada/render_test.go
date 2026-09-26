// Package telapesada holds the EXPENSIVE screen pins — the ones that bring up a
// browser and render the whole page.
//
// Its own package for an operational reason: the deploy gate charges by PACKAGE,
// not by test. This pin alone costs ~33 s of the ~53 s of internal/webassets, and
// it was only because of it that the whole package stayed out of the gate —
// leaving cheap and important pins, like the paste one (3 s), WITHOUT deploy
// coverage. Separated, the gate watches ./internal/webassets (without /...) in
// ~20 s and this one goes on running under `go test ./...` and on pre-push.
//
// In other words: nothing lost surveillance. What changed was WHERE each cost is charged.
package telapesada

import (
	"testing"

	"server-control-panel/internal/webassets/pinos"
)

// TestTelaProxmoxRenderizaNoNavegador is the pin the other two could not be: it
// OPENS THE SCREEN IN A BROWSER and fails on a console error, on a page error
// and on an empty drawing.
//
// 🔴 WHY IT HAD TO EXIST. The expression harnesses passed GREEN, twice in a row,
// over a screen the operator watched break. The defect was in no expression — it
// was in DOM semantics: a `<template>` inside an `<svg>` is not an
// HTMLTemplateElement, has no `.content` and is not inert, so Alpine's x-for blew
// up and the children were rendered with the loop variable out of scope.
// Evaluating expressions in isolation could never see that.
//
// The rule that stays: an expression is proven by evaluating; a SCREEN is proven
// by rendering. It runs in BOTH packages because both can go live: the server
// delivers the `.min.js` when it exists (internal/api/api.go) and falls back to
// the source when it does not. Testing only one of them leaves the other without
// a guard — and the minified one is, precisely, what the operator receives.
func TestTelaProxmoxRenderizaNoNavegador(t *testing.T) {
	for _, pacote := range []string{"min", "src"} {
		t.Run(pacote, func(t *testing.T) {
			pinos.Roda(t, "test-proxmox-render.mjs", "VPSM_RENDER_BUNDLE="+pacote)
		})
	}
}
