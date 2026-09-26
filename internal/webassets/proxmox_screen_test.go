package webassets

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"server-control-panel/internal/webassets/pins"
)

// proxmox_screen_test.go — anchors the screen pins in `go test`.
//
// 🔴 WHY THIS FILE EXISTS
//
// `scripts/test-proxmox-tab.mjs` had 300+ assertions about the Proxmox tab and
// was NOT invoked by anything: not the Makefile, not a hook, not the
// `agentctl deploy` gate. A pin that only runs when somebody remembers to run it
// is not a guard — it is executable documentation nobody executes. That is
// exactly why it stayed green while the tab opened BLACK in production: nobody
// had run it.
//
// Here both screen harnesses come to run inside `go test ./...`, which is what
// the deploy gate already runs.
//
// A missing node is a FAILURE, not a skip. `make minify` already depends on
// node/esbuild, so the machine that builds this project has node; a silent skip
// would return the pin to its orphan state, now disguised as green.

// runHarnessBash is runHarness for pins written in shell (not everything that
// needs asserting is JavaScript — the independence of the recovery container
// lives in a Dockerfile and a startup script).
func runHarnessBash(t *testing.T, script string) {
	t.Helper()
	pins.RunBash(t, script)
}

func runHarness(t *testing.T, script string, env ...string) {
	t.Helper()
	pins.Run(t, script, env...)
}

// TestProxmoxScreenMasterDetail covers the contextual tabs, the lab summary and
// the automatic pause of the live cycle.
func TestProxmoxScreenMasterDetail(t *testing.T) { runHarness(t, "test-proxmox-screen.mjs") }

// TestProxmoxScreenContentInvariants is the harness that already existed and
// was orphaned: states, hysteresis, ANDed filter, grey over dead data, the two
// clocks, and the tab resolution.
func TestProxmoxScreenContentInvariants(t *testing.T) { runHarness(t, "test-proxmox-tab.mjs") }

// TestNoTemplateInsideSVG closes the CLASS, not the case.
//
// The rendering pin catches the symptom where it happens today. This one catches
// the cause anywhere in the file, including in a chart that does not exist yet.
// The two together: one says "it broke", the other says "do not write that".
//
// Comments are stripped before the scan — on purpose. A comment does not render,
// and the explanation of this very defect needs to quote the tags.
func TestNoTemplateInsideSVG(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("web", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	comment := regexp.MustCompile(`(?s)<!--.*?-->`)
	// preserves the line count so the number reported is the real one
	html := comment.ReplaceAllStringFunc(string(raw), func(c string) string {
		return strings.Repeat("\n", strings.Count(c, "\n"))
	})

	svgs := regexp.MustCompile(`(?i)<svg\b`).FindAllStringIndex(html, -1)
	if len(svgs) == 0 {
		t.Fatal("no <svg> in index.html — the scan would pass by vacuity")
	}
	findings := 0
	for _, s := range svgs {
		fim := strings.Index(html[s[0]:], "</svg>")
		if fim < 0 {
			continue
		}
		block := html[s[0] : s[0]+fim]
		for _, m := range regexp.MustCompile(`(?i)<template\b`).FindAllStringIndex(block, -1) {
			line := strings.Count(html[:s[0]+m[0]], "\n") + 1
			t.Errorf("index.html:%d — <template> DENTRO de <svg>. No namespace SVG isso "+
				"não é HTMLTemplateElement: não tem .content, não é inerte, o x-for do Alpine "+
				"estoura no importNode e os filhos renderizam com a variável do laço fora de "+
				"escopo. Use vários subcaminhos num <path> só (cada M abre um subcaminho novo) "+
				"ou escreva os elementos, se forem poucos e constantes.", line)
			findings++
		}
	}
	t.Logf("%d <svg> scanned, %d <template> inside them", len(svgs), findings)
}
