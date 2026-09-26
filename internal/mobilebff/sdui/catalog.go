package sdui

import (
	"sort"
	"strings"
	"sync"
)

// catalog.go — the screen CATALOG: what the app shows in a section picker, so
// that a new screen on the server appears on the phone without an app
// release.
//
// WHY A STATIC CATALOG, AND NOT "BUILD EVERY SCREEN"
// The obvious alternative — assembling the 25 screens on every opening of the
// menu and omitting the ones that return ErrScreenNotFound — would have RBAC
// correct by construction, but it would hang the aggregate cost of ALL the
// builders on a very high-frequency gesture: five calls to the Docker daemon,
// reading /proc, systemd, the secrets vault, the audit log and one OUTBOUND
// HTTP call to Jira. A wobble in Jira would freeze the opening of the menu,
// and nobody would understand why. The catalog runs no builder at all: it
// only consults an in-memory visibility predicate.
//
// THE PRICE OF THAT CHOICE, AND HOW IT IS PAID
// A static catalog is, by definition, a SECOND declaration about permission,
// and two declarations diverge the day somebody touches an `if` in a builder
// without remembering this file. That risk is closed by a table test
// (catalog_visibility_test.go) that walks RegisteredScreens() ×
// {admin, non-admin} and demands `present in the catalog ⟺ Build != ErrScreenNotFound`.
// The test is the reason this file is acceptable; without it, this would be a
// parallel source of truth about authorization.
//
// THE CATALOG IS NEVER THE AUTHORIZATION GATE
// It is a NAVIGATION HINT. What rules is always the builder: GET
// /screens/{id} remains the real decision, and a 404 there is the normal
// behaviour for someone who lost permission between the listing and the tap —
// not an error. If catalog and builder disagree, the builder wins and the
// user simply does not open the screen.

// CatalogEntry is one row of the catalog: the screen's identity plus the
// minimum a picker needs to draw it grouped. It deliberately carries neither
// an icon nor a row count — the icon is a platform decision (the app maps
// group→icon) and a count would demand precisely the Build this catalog
// exists to avoid.
type CatalogEntry struct {
	ID    string `json:"id"`
	Group string `json:"group"`
	Label string `json:"label"`
}

// The catalog's groups. They are a CLOSED set for the same reason the
// component vocabulary is closed (see component.go): a new group is a product
// decision about how the operator sees the machine, reviewed on purpose,
// never a loose string that turns up because somebody typed it differently.
// RegisterCatalog panics on a group outside this list, which turns a typo
// into a boot failure instead of an orphan group with one screen inside it.
const (
	GroupDocker       = "Docker"
	GroupSystem       = "System"
	GroupSecurity     = "Security"
	GroupAutomation   = "Automation"
	GroupIntegrations = "Integrations"
)

// catalogGroupOrder is the ORDER in which the groups appear in the picker,
// and it is deliberate: Docker and System first because they are what you
// look at to know whether the machine is healthy; Security next; Automation
// and Integrations last, because they are what you configure once and revisit
// rarely.
var catalogGroupOrder = []string{
	GroupDocker,
	GroupSystem,
	GroupSecurity,
	GroupAutomation,
	GroupIntegrations,
}

// catalogRegistration is an entry plus the predicate that decides whether it
// appears for a Viewer. visible MUST mirror the gate in the same screen's
// builder — that is what the table test verifies.
type catalogRegistration struct {
	entry   CatalogEntry
	visible func(Viewer) bool
}

var (
	catalogMu sync.Mutex
	catalog   = map[string]catalogRegistration{}
)

// RegisterCatalog enrolls a screen's catalog entry. Call it from the SAME
// place that calls Register() for its builder — that proximity is what makes
// somebody touching the builder's gate see this predicate on the same editor
// screen.
//
// visible is MANDATORY and must be the builder's own predicate: a screen the
// builder refuses with ErrScreenNotFound for non-admins gets
// `func(v Viewer) bool { return v.IsAdmin() }`; a screen the builder
// assembles for any viewer (even with fewer rows or fewer actions) gets an
// always-true predicate, because it IS reachable.
//
// It panics — at boot, never on the first request — on an empty id/label, a
// nil visible, a group outside catalogGroupOrder, or a duplicate id.
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

// CatalogFor returns the entries visible to v, already sorted: groups in
// catalogGroupOrder order, and within each group by label. Determinism
// matters because the app draws the list in the order it arrives — an order
// that changed between requests would make items jump around under the
// finger.
//
// FILTERING BY OMISSION: a non-visible entry is simply absent from the result
// — never present with a "disabled" field. A disabled item confirms the
// screen exists and hands back the enumeration of the administrative surface
// that handlers_screens.go's 404 exists to deny.
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

// CatalogIDs returns the ids of every screen WITH a catalog entry, in
// alphabetical order and regardless of visibility. It exists so the table
// test can point at a screen registered in Register() that nobody cataloged —
// it would be silently unreachable from the picker, which is exactly the
// defect this whole package is fixing.
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
