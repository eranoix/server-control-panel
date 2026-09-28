package webassets

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const (
	shellFile = "web/vendor/panel/app/00-shell.js"
	indexFile = "web/index.html"
)

var (
	reRemapEntry      = regexp.MustCompile(`(?m)^\s*([A-Za-z0-9_]+):\s*\[\s*'(\w+)'\s*,\s*'([\w-]+)'\s*\]`)
	reDefaultsEntry   = regexp.MustCompile(`(?m)^\s*([A-Za-z0-9_]+):\s*'([\w-]+)'`)
	reTabLine         = regexp.MustCompile(`(?m)^.*setTab\('([\w-]+)'\).*$`)
	reGroupInLine     = regexp.MustCompile(`tabs\.(\w+)`)
	reDeclaredSection = regexp.MustCompile(`currentView\s*===\s*'([\w-]+)'`)
	reTabToView       = regexp.MustCompile(`(?s)tabToView\(group, tab\) \{(.*?)\n    \},`)
)

func block(t *testing.T, source, name string) string {
	t.Helper()
	i := strings.Index(source, name+": {")
	if i < 0 {
		t.Fatalf("block %q not found in %s — the shell changed shape and this guard went blind", name, shellFile)
	}
	j := strings.Index(source[i:], "\n    },")
	if j < 0 {
		t.Fatalf("end of block %q not found in %s", name, shellFile)
	}
	return source[i : i+j]
}

func resolveTab(remap map[string][2]string, order []string, group, tab string, canonicalFirst bool) string {
	if canonicalFirst {
		if c, ok := remap[tab]; ok && c[0] == group && c[1] == tab {
			return tab
		}
	}
	for _, view := range order {
		c := remap[view]
		if c[0] == group && c[1] == tab {
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

	if len(remap) < 10 || len(defaults) < 3 {
		t.Fatalf("insufficient coverage: PAGE_REMAP=%d GROUP_DEFAULTS=%d — the regexes stopped matching and the guard went blind", len(remap), len(defaults))
	}

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
		tab := line[1]
		g := reGroupInLine.FindStringSubmatch(line[0])
		if g == nil {
			navigations = append(navigations, tab)
			continue
		}
		tabs[g[1]+"|"+tab] = true
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
		group, tab := parts[0], parts[1]
		if _, hasDefault := defaults[group]; !hasDefault {
			continue
		}
		checked++
		view := resolveTab(remap, order, group, tab, canonicalFirst)
		if !sections[view] {
			broken = append(broken, fmt.Sprintf("%s → %s  resolves to currentView=%q, and NO section x-show matches (the screen opens BLACK)", group, tab, view))
		}
	}
	if checked < 30 {
		t.Fatalf("only %d tabs checked — coverage too thin for this guard to be worth anything", checked)
	}
	if len(broken) > 0 {
		t.Errorf("%d of %d tabs render nothing:\n  %s", len(broken), checked, strings.Join(broken, "\n  "))
		return
	}
	t.Logf("%d tabs checked against %d sections (shell rule: canonical-first=%v); all of them resolve", checked, len(sections), canonicalFirst)
}

func TestShellTabToViewIsDeterministic(t *testing.T) {
	shell := readSource(t, shellFile)
	m := reTabToView.FindStringSubmatch(shell)
	if m == nil {
		t.Fatalf("method tabToView not found in %s — the guard went blind", shellFile)
	}
	body := m[1]
	if !strings.Contains(body, "this.PAGE_REMAP[tab]") {
		t.Errorf("tabToView scans again without preferring the canonical key.\n"+
			"Without `this.PAGE_REMAP[tab]` the answer depends on the ORDER the keys are written in,\n"+
			"and a tab with an alias (e.g. nodes/proxmox) resolves to the alias instead of the\n"+
			"canonical key, which renders the Proxmox tab BLACK.\nbody read:\n%s", body)
	}
}

func TestPageRemapAliasesStayAlive(t *testing.T) {
	shell := readSource(t, shellFile)
	remap := map[string][2]string{}
	for _, m := range reRemapEntry.FindAllStringSubmatch(block(t, shell, "PAGE_REMAP"), -1) {
		remap[m[1]] = [2]string{m[2], m[3]}
	}
	for _, alias := range []struct{ key, group, tab string }{
		{"nodes", "operations", "proxmox"},
	} {
		got, ok := remap[alias.key]
		if !ok {
			t.Errorf("the alias %q disappeared from PAGE_REMAP — an old link, a bookmark and the command palette now land nowhere", alias.key)
			continue
		}
		if got[0] != alias.group || got[1] != alias.tab {
			t.Errorf("the alias %q points at %v, expected [%s %s]", alias.key, got, alias.group, alias.tab)
		}
	}
}
