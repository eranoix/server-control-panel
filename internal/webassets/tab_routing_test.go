package webassets

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// tab_routing_test.go — the pin the Proxmox tab demanded by opening BLACK.
//
// 🔴 WHY THIS TEST EXISTS
//
// The Proxmox tab opened black in production. The invariant that should have
// caught it was TEXTUAL: it counted the string `currentView==='proxmox'` in the
// index.html. The section EXISTED — it just never RENDERED, because `tabToView`
// resolved that tab to 'nodes'. The grep passed green over a black screen.
//
// This test does not look for text: it REIMPLEMENTS the tab->view resolution
// from the maps read out of 00-shell.js itself, and demands that EVERY tab
// declared in index.html lands on a <section x-show="currentView==='X'"> that
// exists. It closes the whole CLASS of the defect, not the case that showed up.
//
// If one day the panel gains a tab with no section, or loses the section of a
// live tab, or reorders PAGE_REMAP in a way that changes the resolution — this
// test fails NAMING the group/tab pair. No operator has to discover that by
// opening the browser.

const (
	shellFile = "web/vendor/vpsm/app/00-shell.js"
	indexFile = "web/index.html"
)

var (
	reRemapEntry    = regexp.MustCompile(`(?m)^\s*([A-Za-z0-9_]+):\s*\[\s*'(\w+)'\s*,\s*'([\w-]+)'\s*\]`)
	reDefaultsEntry = regexp.MustCompile(`(?m)^\s*([A-Za-z0-9_]+):\s*'([\w-]+)'`)
	// The source of truth for "a tab button exists" is the setTab('X') call.
	// Checking only `tabs.G==='A'` left 7 of the 40 tabs out: the Containers button,
	// for instance, is written `(tabs.docker||'containers')==='containers'`.
	// The group comes from the `tabs.<group>` that appears on the SAME line as the button.
	reTabLine         = regexp.MustCompile(`(?m)^.*setTab\('([\w-]+)'\).*$`)
	reGroupInLine     = regexp.MustCompile(`tabs\.(\w+)`)
	reDeclaredSection = regexp.MustCompile(`currentView\s*===\s*'([\w-]+)'`)
	reTabToView       = regexp.MustCompile(`(?s)tabToView\(group, tab\) \{(.*?)\n    \},`)
)

// bloco slices a level-2 object literal out of the shell (`    NOME: {` up to `\n    },`).
func block(t *testing.T, source, nome string) string {
	t.Helper()
	i := strings.Index(source, nome+": {")
	if i < 0 {
		t.Fatalf("block %q not found in %s — the shell changed shape and this guard went blind", nome, shellFile)
	}
	j := strings.Index(source[i:], "\n    },")
	if j < 0 {
		t.Fatalf("end of block %q not found in %s", nome, shellFile)
	}
	return source[i : i+j]
}

// resolverAba reimplements tabToView from 00-shell.js.
//
// The rule: the CANONICAL key of a tab is the one with the SAME NAME as the tab;
// aliases (several keys for the same destination) are a fallback. Without that
// the answer would depend on the order the keys were written in, which is an
// accident and not a contract — that is exactly how the screen went black.
//
// canonicalFirst reflects what 00-shell.js REALLY does today: it is read from
// the file, not assumed. If somebody reverts the JS to the naive scan, this
// resolver reverts with it and the test fails NAMING the tab that goes black —
// instead of staying green because the right rule lives only in the Go.
func resolverAba(remap map[string][2]string, order []string, group, aba string, canonicalFirst bool) string {
	if canonicalFirst {
		if c, ok := remap[aba]; ok && c[0] == group && c[1] == aba {
			return aba
		}
	}
	for _, view := range order {
		c := remap[view]
		if c[0] == group && c[1] == aba {
			return view
		}
	}
	return group
}

func readSource(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("could not read %s: %v", path, err)
	}
	return string(b)
}

// TestEveryTabResolvesToAnExistingSection is the main pin.
func TestEveryTabResolvesToAnExistingSection(t *testing.T) {
	shell := readSource(t, shellFile)
	index := readSource(t, indexFile)

	remap := map[string][2]string{}
	var order []string
	for _, m := range reRemapEntry.FindAllStringSubmatch(block(t, shell, "PAGE_REMAP"), -1) {
		remap[m[1]] = [2]string{m[2], m[3]}
		order = append(order, m[1])
	}
	defaults := map[string]string{}
	for _, m := range reDefaultsEntry.FindAllStringSubmatch(block(t, shell, "GROUP_DEFAULTS"), -1) {
		defaults[m[1]] = m[2]
	}

	// Coverage guard: a pin that reads zero entries protects nothing. A regex that
	// stopped matching would come out "green" without checking a single tab — the
	// same vacuity this test exists in order not to repeat.
	if len(remap) < 10 || len(defaults) < 3 {
		t.Fatalf("insufficient coverage: PAGE_REMAP=%d GROUP_DEFAULTS=%d — the regexes stopped matching and the guard went blind", len(remap), len(defaults))
	}

	// Read from the shell ITSELF which rule is in force, instead of presuming the right one.
	mt := reTabToView.FindStringSubmatch(shell)
	if mt == nil {
		t.Fatalf("method tabToView not found in %s — the guard went blind", shellFile)
	}
	canonicalFirst := strings.Contains(mt[1], "this.PAGE_REMAP[tab]")

	sections := map[string]bool{}
	for _, m := range reDeclaredSection.FindAllStringSubmatch(index, -1) {
		sections[m[1]] = true
	}
	tabs := map[string]bool{}
	var navigations []string
	for _, line := range reTabLine.FindAllStringSubmatch(index, -1) {
		aba := line[1]
		g := reGroupInLine.FindStringSubmatch(line[0])
		if g == nil {
			// A setTab without `tabs.<group>` on the line is not the DECLARATION of a
			// tab: it is NAVIGATION to it (a "Go to Compose" button, a tooltip
			// shortcut, or a mention inside a comment). We keep the target to check
			// just below that it exists as a real tab — navigating to a tab nobody
			// declares leads nowhere.
			navigations = append(navigations, aba)
			continue
		}
		tabs[g[1]+"|"+aba] = true
	}
	declared := map[string]bool{}
	for par := range tabs {
		declared[strings.SplitN(par, "|", 2)[1]] = true
	}
	var orphans []string
	for _, target := range navigations {
		if !declared[target] {
			orphans = append(orphans, target)
		}
	}
	if len(orphans) > 0 {
		sort.Strings(orphans)
		t.Errorf("%d setTab call(s) navigate to a tab NOBODY declares — the click leads nowhere: %s",
			len(orphans), strings.Join(orphans, ", "))
	}
	if len(tabs) < 10 || len(sections) < 10 {
		t.Fatalf("insufficient coverage: tabs=%d sections=%d in %s — the guard went blind", len(tabs), len(sections), indexFile)
	}

	var pares []string
	for p := range tabs {
		pares = append(pares, p)
	}
	sort.Strings(pares)

	var broken []string
	checked := 0
	for _, p := range pares {
		parts := strings.SplitN(p, "|", 2)
		group, aba := parts[0], parts[1]
		if _, temDefault := defaults[group]; !temDefault {
			continue // group with no tabs: currentView is the page itself
		}
		checked++
		view := resolverAba(remap, order, group, aba, canonicalFirst)
		if !sections[view] {
			broken = append(broken, fmt.Sprintf("%s → %s  resolves to currentView=%q, and NO section x-show matches (the screen opens BLACK)", group, aba, view))
		}
	}
	if checked < 30 {
		t.Fatalf("only %d tabs checked — coverage too thin for this guard to be worth anything", checked)
	}
	if len(broken) > 0 {
		t.Errorf("%d of %d tabs render nothing:\n  %s", len(broken), checked, strings.Join(broken, "\n  "))
		return // do not announce "all resolve" right below a failure
	}
	t.Logf("%d tabs checked against %d sections (shell rule: canonical-first=%v); all of them resolve", checked, len(sections), canonicalFirst)
}

// TestShellTabToViewIsDeterministic proves that the SERVED FILE uses the
// canonical-key rule. Without this counterpart, somebody could revert
// 00-shell.js to the naive scan and the test above would keep passing — because
// it reimplements the rule in Go instead of reading the JS.
func TestShellTabToViewIsDeterministic(t *testing.T) {
	shell := readSource(t, shellFile)
	m := reTabToView.FindStringSubmatch(shell)
	if m == nil {
		t.Fatalf("method tabToView not found in %s — the guard went blind", shellFile)
	}
	body := m[1]
	if !strings.Contains(body, "this.PAGE_REMAP[tab]") {
		t.Errorf("tabToView voltou a varrer sem preferir a key canonical.\n"+
			"Sem `this.PAGE_REMAP[tab]` a resposta depende da ORDEM DE ESCRITA das chaves,\n"+
			"e uma aba com alias (hoje: nodes/proxmox) resolve para o alias em vez da\n"+
			"canonica — que foi como a aba Proxmox abriu PRETA (quick 260820-95v).\ncorpo lido:\n%s", body)
	}
}

// TestPageRemapAliasesStayAlive protects the other half: the legacy alias
// must NOT be deleted. An old link, a bookmark, a memorized shortcut and the
// command-palette entry all depend on `nodes` landing on the screen that
// inherited the content. Deleting the key would send them all nowhere.
func TestPageRemapAliasesStayAlive(t *testing.T) {
	shell := readSource(t, shellFile)
	remap := map[string][2]string{}
	for _, m := range reRemapEntry.FindAllStringSubmatch(block(t, shell, "PAGE_REMAP"), -1) {
		remap[m[1]] = [2]string{m[2], m[3]}
	}
	for _, alias := range []struct{ key, group, aba string }{
		{"nodes", "operations", "proxmox"},
	} {
		got, ok := remap[alias.key]
		if !ok {
			t.Errorf("the alias %q disappeared from PAGE_REMAP — an old link, a bookmark and the command palette now land nowhere", alias.key)
			continue
		}
		if got[0] != alias.group || got[1] != alias.aba {
			t.Errorf("the alias %q points at %v, expected [%s %s]", alias.key, got, alias.group, alias.aba)
		}
	}
}
