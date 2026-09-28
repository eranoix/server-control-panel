package sdui

import (
	"sort"
	"strings"
	"sync"
)

type CatalogEntry struct {
	ID    string `json:"id"`
	Group string `json:"group"`
	Label string `json:"label"`
}

const (
	GroupDocker       = "Docker"
	GroupSystem       = "System"
	GroupSecurity     = "Security"
	GroupAutomation   = "Automation"
	GroupIntegrations = "Integrations"
)

var catalogGroupOrder = []string{
	GroupDocker,
	GroupSystem,
	GroupSecurity,
	GroupAutomation,
	GroupIntegrations,
}

type catalogRegistration struct {
	entry   CatalogEntry
	visible func(Viewer) bool
}

var (
	catalogMu sync.Mutex
	catalog   = map[string]catalogRegistration{}
)

func RegisterCatalog(id, group, label string, visible func(Viewer) bool) {
	if id == "" {
		panic("sdui: RegisterCatalog: empty id")
	}
	if label == "" {
		panic("sdui: RegisterCatalog(" + id + "): empty label")
	}
	if visible == nil {
		panic("sdui: RegisterCatalog(" + id + "): visible is required — no catalog entry may exist without an explicit visibility decision")
	}
	if !validCatalogGroup(group) {
		panic("sdui: RegisterCatalog(" + id + "): unknown group: " + group)
	}
	catalogMu.Lock()
	defer catalogMu.Unlock()
	if _, exists := catalog[id]; exists {
		panic("sdui: RegisterCatalog: already registered: " + id)
	}
	catalog[id] = catalogRegistration{
		entry:   CatalogEntry{ID: id, Group: group, Label: label},
		visible: visible,
	}
}

func validCatalogGroup(group string) bool {
	for _, g := range catalogGroupOrder {
		if g == group {
			return true
		}
	}
	return false
}

func CatalogFor(v Viewer) []CatalogEntry {
	catalogMu.Lock()
	entries := make([]CatalogEntry, 0, len(catalog))
	for _, reg := range catalog {
		if reg.visible(v) {
			entries = append(entries, reg.entry)
		}
	}
	catalogMu.Unlock()

	groupRank := make(map[string]int, len(catalogGroupOrder))
	for i, g := range catalogGroupOrder {
		groupRank[g] = i
	}
	sort.Slice(entries, func(i, j int) bool {
		if gi, gj := groupRank[entries[i].Group], groupRank[entries[j].Group]; gi != gj {
			return gi < gj
		}
		if c := strings.Compare(entries[i].Label, entries[j].Label); c != 0 {
			return c < 0
		}
		return entries[i].ID < entries[j].ID
	})
	return entries
}

func CatalogIDs() []string {
	catalogMu.Lock()
	defer catalogMu.Unlock()
	ids := make([]string, 0, len(catalog))
	for id := range catalog {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
