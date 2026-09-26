package mobilebff

import (
	"path/filepath"
	"testing"

	"server-control-panel/internal/notify"
)

func TestDeviceTokenStore_UpsertIsIdempotentByDeviceID(t *testing.T) {
	dir := t.TempDir()
	s := NewDeviceTokenStore(dir)

	isNew, err := s.Upsert("sam", DeviceEntry{DeviceID: "dev-1", FCMToken: "tok-a", Platform: "android"})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if !isNew {
		t.Fatalf("first Upsert of dev-1 must report isNew=true")
	}

	isNew, err = s.Upsert("sam", DeviceEntry{DeviceID: "dev-1", FCMToken: "tok-b", Platform: "android"})
	if err != nil {
		t.Fatalf("Upsert (update): %v", err)
	}
	if isNew {
		t.Fatalf("second Upsert of the SAME device_id must report isNew=false")
	}

	list, err := s.List("sam")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].FCMToken != "tok-b" {
		t.Fatalf("List = %#v, want exactly one device with the updated token", list)
	}
}

func TestDeviceTokenStore_RemoveThenListEmpty(t *testing.T) {
	dir := t.TempDir()
	s := NewDeviceTokenStore(dir)
	if _, err := s.Upsert("sam", DeviceEntry{DeviceID: "dev-1", FCMToken: "tok-a"}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := s.Remove("sam", "dev-1"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	list, err := s.List("sam")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("List after Remove = %#v, want empty", list)
	}
}

func TestDeviceTokenStore_AllTokensSpansEveryUser(t *testing.T) {
	dir := t.TempDir()
	s := NewDeviceTokenStore(dir)
	if _, err := s.Upsert("sam", DeviceEntry{DeviceID: "dev-a", FCMToken: "tok-a"}); err != nil {
		t.Fatalf("Upsert sam: %v", err)
	}
	if _, err := s.Upsert("bob", DeviceEntry{DeviceID: "dev-b", FCMToken: "tok-b"}); err != nil {
		t.Fatalf("Upsert bob: %v", err)
	}
	all := s.AllTokens()
	if len(all) != 2 {
		t.Fatalf("AllTokens = %#v, want 2 entries across both users", all)
	}
	byDevice := map[string]string{}
	for _, tk := range all {
		byDevice[tk.DeviceID] = tk.Token
	}
	if byDevice["dev-a"] != "tok-a" || byDevice["dev-b"] != "tok-b" {
		t.Fatalf("AllTokens tokens = %#v", byDevice)
	}
}

func TestDeviceTokenStore_TokensForUserOnlyThatUser(t *testing.T) {
	dir := t.TempDir()
	s := NewDeviceTokenStore(dir)
	if _, err := s.Upsert("sam", DeviceEntry{DeviceID: "dev-a", FCMToken: "tok-a"}); err != nil {
		t.Fatalf("Upsert sam: %v", err)
	}
	if _, err := s.Upsert("bob", DeviceEntry{DeviceID: "dev-b", FCMToken: "tok-b"}); err != nil {
		t.Fatalf("Upsert bob: %v", err)
	}
	got := s.TokensForUser("sam")
	if len(got) != 1 || got[0].DeviceID != "dev-a" {
		t.Fatalf("TokensForUser(sam) = %#v, want only dev-a", got)
	}
}

func TestDeviceTokenStorePath_IsPerUser(t *testing.T) {
	p1 := DeviceTokenStorePath("/data", "sam")
	p2 := DeviceTokenStorePath("/data", "bob")
	if p1 == p2 {
		t.Fatalf("paths must differ per user, got %q == %q", p1, p2)
	}
	if filepath.Dir(p1) != "/data" {
		t.Fatalf("path = %q, want under /data", p1)
	}
}

// ── DevicePrefsStore ─────────────────────────────────────────────────────────

func TestDevicePrefs_PutThenGetRoundTrips(t *testing.T) {
	dir := t.TempDir()
	s := NewDevicePrefsStore(dir)
	if err := s.Put("sam", DevicePref{DeviceID: "dev-1", EnabledRuleIDs: []string{"r1", "r2"}}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	pref, ok, err := s.Get("sam", "dev-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatalf("Get ok=false, want true after Put")
	}
	if len(pref.EnabledRuleIDs) != 2 {
		t.Fatalf("EnabledRuleIDs = %#v", pref.EnabledRuleIDs)
	}
}

func TestDevicePrefs_GetUnknownDeviceReportsNotOK(t *testing.T) {
	dir := t.TempDir()
	s := NewDevicePrefsStore(dir)
	_, ok, err := s.Get("sam", "never-registered")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ok {
		t.Fatalf("Get ok=true for a device that was never Put, want false")
	}
}

// TestDevicePrefs_AllowedIsPerRuleAndPerDevice is Task 3's literal <done>
// criterion: a device with the rule excluded is skipped while a device with
// it enabled still passes — proving the filter is per-device AND per-Rule.
func TestDevicePrefs_AllowedIsPerRuleAndPerDevice(t *testing.T) {
	dir := t.TempDir()
	s := NewDevicePrefsStore(dir)
	if err := s.Put("sam", DevicePref{DeviceID: "dev-excluded", EnabledRuleIDs: []string{"other-rule"}}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := s.Put("sam", DevicePref{DeviceID: "dev-included", EnabledRuleIDs: []string{"target-rule"}}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	if s.Allowed("dev-excluded", "target-rule") {
		t.Fatalf("dev-excluded must be blocked for target-rule (not in its EnabledRuleIDs)")
	}
	if !s.Allowed("dev-included", "target-rule") {
		t.Fatalf("dev-included must be allowed for target-rule (explicitly enabled)")
	}
}

// TestDevicePrefs_UnknownDeviceAllowedAcrossAllUsers proves Allowed scans
// every user's file and fails OPEN for a device_id it never finds anywhere
// — the fail-open-for-unknown-device guarantee (protects pre-existing
// webpush subscriptions from a silent default-critical-only regression).
func TestDevicePrefs_UnknownDeviceAllowedAcrossAllUsers(t *testing.T) {
	dir := t.TempDir()
	s := NewDevicePrefsStore(dir)
	if err := s.Put("sam", DevicePref{DeviceID: "dev-sam", EnabledRuleIDs: nil}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if !s.Allowed("never-seen-device", "any-rule") {
		t.Fatalf("an unregistered device must fail OPEN (allowed), not default to blocked/critical-only")
	}
}

func TestDevicePrefs_SeedDefaultsEnablesOnlyCriticalRules(t *testing.T) {
	dir := t.TempDir()
	router, err := notify.New(notify.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("notify.New: %v", err)
	}
	t.Cleanup(router.Close)
	if _, err := router.UpsertRule(notify.Rule{Name: "critico", Enabled: true, MinSeverity: notify.SeverityCritical, Channels: []string{"push"}}); err != nil {
		t.Fatalf("UpsertRule critico: %v", err)
	}
	if _, err := router.UpsertRule(notify.Rule{Name: "chato", Enabled: true, MinSeverity: notify.SeverityInfo, Channels: []string{"push"}}); err != nil {
		t.Fatalf("UpsertRule chato: %v", err)
	}

	s := NewDevicePrefsStore(dir)
	if err := s.SeedDefaults("sam", "dev-1", router); err != nil {
		t.Fatalf("SeedDefaults: %v", err)
	}

	var criticalID, chattyID string
	for _, rl := range router.Rules() {
		switch rl.Name {
		case "critico":
			criticalID = rl.ID
		case "chato":
			chattyID = rl.ID
		}
	}
	if !s.Allowed("dev-1", criticalID) {
		t.Fatalf("seeded defaults must enable the critical rule")
	}
	if s.Allowed("dev-1", chattyID) {
		t.Fatalf("seeded defaults must NOT enable the non-critical rule")
	}
}
