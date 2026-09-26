package sdui

import (
	"context"
	"errors"
	"sort"
	"sync"
)

// CurrentSDUIVersion is the version number of the ENVELOPE itself — not of
// the component vocabulary. It only goes up on a breaking change to the
// envelope format; adding a new component type never bumps it (see the
// Envelope comment in component.go). The registry stamps this value on every
// Build, so that no Builder can declare a different version by mistake.
const CurrentSDUIVersion = 1

// Builder assembles a screen's Envelope for a specific Viewer. A Builder is
// where RBAC filtering actually happens: looking at v, it decides which
// components/fields/actions go into the returned Envelope — it never emits
// something and marks it hidden.
type Builder func(ctx context.Context, v Viewer) (*Envelope, error)

// ErrScreenNotFound is returned by Build both for an id that was never
// registered and — by the Builder itself — for an id that exists but that
// this Viewer cannot see. The two cases must be indistinguishable to Build's
// caller, because that very indistinguishability is what prevents enumeration
// of the administrative surface (see internal/mobilebff/handlers_screens.go).
var ErrScreenNotFound = errors.New("sdui: screen not found")

var (
	registryMu sync.Mutex
	registry   = map[string]Builder{}
)

// Register enrolls a screen's Builder under a stable id. It is the ONLY
// extension point for adding a new screen: one call to Register plus one
// Builder function, with no change whatsoever to the Kotlin client. If a
// screen needs something the 7 vocabulary types cannot express, the answer is
// a dedicated native module, never an eighth component type (see the package
// comment in component.go).
//
// Call it from an init() in the file that defines the screen's Builder — two
// registrations with the same id are a programming error and must blow up at
// boot, never silently on the first request.
func Register(id string, b Builder) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registry[id]; exists {
		panic("sdui: screen already registered: " + id)
	}
	registry[id] = b
}

// Build resolves and assembles screen id's Envelope for Viewer v. It returns
// ErrScreenNotFound both for an unknown id and for any error the Builder
// itself returns signalling "not visible to this Viewer" — see the
// ErrScreenNotFound comment.
//
// The returned Envelope's SDUIVersion is always CurrentSDUIVersion, whatever
// the Builder filled in: no Builder may declare an envelope version of its
// own.
func Build(ctx context.Context, id string, v Viewer) (*Envelope, error) {
	registryMu.Lock()
	b, ok := registry[id]
	registryMu.Unlock()
	if !ok {
		return nil, ErrScreenNotFound
	}
	env, err := b(ctx, v)
	if err != nil {
		return nil, err
	}
	env.SDUIVersion = CurrentSDUIVersion
	return env, nil
}

// RegisteredScreens returns the ids of every registered screen, in
// alphabetical order — deterministic on purpose, so that an automated check
// can enumerate every screen without depending on package initialization
// order.
func RegisteredScreens() []string {
	registryMu.Lock()
	defer registryMu.Unlock()
	ids := make([]string, 0, len(registry))
	for id := range registry {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
