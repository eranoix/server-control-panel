package sdui

import "sync"

// forbiddenForNonAdminExt is the same registry as golden_test.go's
// forbiddenForNonAdmin, but for screens defined OUTSIDE this package (e.g.
// internal/mobilebff/screens). golden_test.go is an internal test file
// (package sdui) — it cannot import internal/mobilebff/screens without
// creating a cycle (screens already imports sdui), so an external screen
// cannot simply add an entry to golden_test.go's map.
// RegisterForbiddenForNonAdmin is the valve: the screen's package calls this
// from the same Register() that enrolls the screen itself, deriving the
// forbidden set from the SAME authorization function the screen's Builder
// used — never a separate hand-picked list.
var (
	forbiddenForNonAdminExtMu sync.Mutex
	forbiddenForNonAdminExt   = map[string]func() []string{}
)

// RegisterForbiddenForNonAdmin enrolls, under screenID, the function that
// returns the strings this screen's non-admin golden may never contain. Call
// it exactly once per screenID — a second call for the same id is a
// programming error and panics at registration, the same policy as
// sdui.Register and sdui.RegisterAction.
func RegisterForbiddenForNonAdmin(screenID string, fn func() []string) {
	if screenID == "" {
		panic("sdui: RegisterForbiddenForNonAdmin: screenID vazio")
	}
	if fn == nil {
		panic("sdui: RegisterForbiddenForNonAdmin(" + screenID + "): fn is required")
	}
	forbiddenForNonAdminExtMu.Lock()
	defer forbiddenForNonAdminExtMu.Unlock()
	if _, exists := forbiddenForNonAdminExt[screenID]; exists {
		panic("sdui: RegisterForbiddenForNonAdmin: already registered: " + screenID)
	}
	forbiddenForNonAdminExt[screenID] = fn
}

// forbiddenForNonAdminExtEntry returns the external entry (registered via
// RegisterForbiddenForNonAdmin) for screenID. golden_test.go (package sdui,
// the same package) merges this with its internal forbiddenForNonAdmin map in
// lookupForbiddenForNonAdmin — this file is not a _test.go, so it can never
// reference a symbol defined only in a test build.
func forbiddenForNonAdminExtEntry(screenID string) (func() []string, bool) {
	forbiddenForNonAdminExtMu.Lock()
	defer forbiddenForNonAdminExtMu.Unlock()
	fn, ok := forbiddenForNonAdminExt[screenID]
	return fn, ok
}
