package sdui

import "sync"

var (
	forbiddenForNonAdminExtMu sync.Mutex
	forbiddenForNonAdminExt   = map[string]func() []string{}
)

func RegisterForbiddenForNonAdmin(screenID string, fn func() []string) {
	if screenID == "" {
		panic("sdui: RegisterForbiddenForNonAdmin: empty screenID")
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

func forbiddenForNonAdminExtEntry(screenID string) (func() []string, bool) {
	forbiddenForNonAdminExtMu.Lock()
	defer forbiddenForNonAdminExtMu.Unlock()
	fn, ok := forbiddenForNonAdminExt[screenID]
	return fn, ok
}
