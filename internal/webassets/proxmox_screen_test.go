package webassets

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"server-control-panel/internal/webassets/pins"
)

func runHarnessBash(t *testing.T, script string) {
	t.Helper()
	pins.RunBash(t, script)
}

func runHarness(t *testing.T, script string, env ...string) {
	t.Helper()
	pins.Run(t, script, env...)
}

func TestProxmoxScreenMasterDetail(t *testing.T) { runHarness(t, "test-proxmox-screen.mjs") }

func TestProxmoxScreenContentInvariants(t *testing.T) { runHarness(t, "test-proxmox-tab.mjs") }

func TestNoTemplateInsideSVG(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("web", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	comment := regexp.MustCompile(`(?s)<!--.*?-->`)
	html := comment.ReplaceAllStringFunc(string(raw), func(c string) string {
		return strings.Repeat("\n", strings.Count(c, "\n"))
	})

	svgs := regexp.MustCompile(`(?i)<svg\b`).FindAllStringIndex(html, -1)
	if len(svgs) == 0 {
		t.Fatal("no <svg> in index.html — the scan would pass by vacuity")
	}
	findings := 0
	for _, s := range svgs {
		end := strings.Index(html[s[0]:], "</svg>")
		if end < 0 {
			continue
		}
		block := html[s[0] : s[0]+end]
		for _, m := range regexp.MustCompile(`(?i)<template\b`).FindAllStringIndex(block, -1) {
			line := strings.Count(html[:s[0]+m[0]], "\n") + 1
			t.Errorf("index.html:%d: <template> INSIDE <svg>. In the SVG namespace it "+
				"is not an HTMLTemplateElement: it has no .content and is not inert, Alpine's x-for "+
				"blows up in importNode and the children render with the loop variable out of "+
				"scope. Use several subpaths in a single <path> (each M starts a new subpath) "+
				"or write the elements out, if they are few and constant.", line)
			findings++
		}
	}
	t.Logf("%d <svg> scanned, %d <template> inside them", len(svgs), findings)
}
