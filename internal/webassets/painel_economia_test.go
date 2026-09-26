package webassets

import "testing"

// painel_economia_test.go — anchors the Economy panel pin in `go test`, which is
// what the `agentctl deploy` gate runs.
//
// Without this anchor the harness would be one more .mjs that only runs when
// somebody remembers — that is how test-proxmox-tab.mjs sat orphaned and green
// for months while the tab opened black in production.
//
// A missing node is a FAILURE, not a skip: `make minify` already depends on
// node/esbuild, so the machine that builds this project has node.
func TestPainelEconomiaResisteAEstadoParcial(t *testing.T) {
	rodaHarness(t, "test-datasaver-painel.mjs")
}
