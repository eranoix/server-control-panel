package mobilebff

// notify_prefs.go — per-device push preferences: which internal/notify.Router
// Rules reach a specific device_id. The default is aligned with alert fatigue:
// only Rules with MinSeverity "critical" reach the user until they opt into
// more.
//
// Persistence: the same pattern as push_devices.go (one JSON file per user
// under DataDir, a mutex, atomic writes), but with a second read mode —
// Allowed(deviceID, ruleID) — that sweeps ALL users, because
// PushChannel.allowDevice (internal/notify/pushchannel.go) only ever receives a
// bare device_id, with no context about which user owns it.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/notify"
)

// DevicePref is one device_id's preference: which Rule IDs are allowed to push
// to it.
type DevicePref struct {
	DeviceID       string   `json:"device_id"`
	EnabledRuleIDs []string `json:"enabled_rule_ids"`
	UpdatedAt      int64    `json:"updated_at"`
}

func (p DevicePref) has(ruleID string) bool {
	for _, id := range p.EnabledRuleIDs {
		if id == ruleID {
			return true
		}
	}
	return false
}

type devicePrefsFile struct {
	SchemaVersion int          `json:"schema_version"`
	User          string       `json:"user"`
	Devices       []DevicePref `json:"devices"`
}

// DevicePrefsStore persists the preferences of ALL users, one JSON file per
// user under dataDir (mobile-notify-prefs-<user>.json). It satisfies
// notify.DevicePrefsResolver (Allowed) structurally — this package imports
// internal/notify only for the Rule/Router types (reading the catalogue), never
// the other way around.
type DevicePrefsStore struct {
	mu      sync.Mutex
	dataDir string
}

// NewDevicePrefsStore creates a store pointing at dataDir.
func NewDevicePrefsStore(dataDir string) *DevicePrefsStore {
	return &DevicePrefsStore{dataDir: dataDir}
}

// DevicePrefsStorePath returns the canonical file path for one user.
func DevicePrefsStorePath(dataDir, user string) string {
	return filepath.Join(strings.TrimRight(dataDir, "/"), fmt.Sprintf("mobile-notify-prefs-%s.json", user))
}

func loadDevicePrefsFile(path string) (*devicePrefsFile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("notify_prefs: read: %w", err)
	}
	var f devicePrefsFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("notify_prefs: parse: %w", err)
	}
	return &f, nil
}

func saveDevicePrefsFile(path string, f *devicePrefsFile) error {
	if len(f.Devices) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("notify_prefs: remove empty: %w", err)
		}
		return nil
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("notify_prefs: marshal: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return fmt.Errorf("notify_prefs: write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("notify_prefs: rename: %w", err)
	}
	return nil
}

// Get returns deviceID's stored preference for user. ok=false when the device
// never had a preference stored (not even a seeded one) — the HTTP caller uses
// that to compute the display default without persisting anything yet.
func (s *DevicePrefsStore) Get(user, deviceID string) (pref DevicePref, ok bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := loadDevicePrefsFile(DevicePrefsStorePath(s.dataDir, user))
	if err != nil || file == nil {
		return DevicePref{}, false, err
	}
	for _, d := range file.Devices {
		if d.DeviceID == deviceID {
			return d, true, nil
		}
	}
	return DevicePref{}, false, nil
}

// Put stores (upserts) deviceID's preference for user.
func (s *DevicePrefsStore) Put(user string, pref DevicePref) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := DevicePrefsStorePath(s.dataDir, user)
	file, err := loadDevicePrefsFile(path)
	if err != nil {
		return err
	}
	if file == nil {
		file = &devicePrefsFile{SchemaVersion: 1, User: user}
	}
	pref.UpdatedAt = time.Now().Unix()
	replaced := false
	for i := range file.Devices {
		if file.Devices[i].DeviceID == pref.DeviceID {
			file.Devices[i] = pref
			replaced = true
			break
		}
	}
	if !replaced {
		file.Devices = append(file.Devices, pref)
	}
	return saveDevicePrefsFile(path, file)
}

// Remove deletes deviceID's preference for user. Idempotent.
func (s *DevicePrefsStore) Remove(user, deviceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := DevicePrefsStorePath(s.dataDir, user)
	file, err := loadDevicePrefsFile(path)
	if err != nil || file == nil {
		return err
	}
	kept := file.Devices[:0]
	for _, d := range file.Devices {
		if d.DeviceID != deviceID {
			kept = append(kept, d)
		}
	}
	file.Devices = kept
	return saveDevicePrefsFile(path, file)
}

// SeedDefaults writes, the first time a device_id shows up (called by
// push_devices.go's registerPushDevice when it detects isNew), the alert-fatigue
// default: only Rules with MinSeverity=="critical" enabled. notifyRouter may be
// nil (e.g. the Router is not mounted yet) — with no Rule catalogue nothing is
// seeded and the device falls into the "unknown" path (fail-open) until an
// explicit preference exists.
func (s *DevicePrefsStore) SeedDefaults(user, deviceID string, notifyRouter *notify.Router) error {
	if notifyRouter == nil {
		return nil
	}
	var enabled []string
	for _, rl := range notifyRouter.Rules() {
		if rl.MinSeverity == notify.SeverityCritical {
			enabled = append(enabled, rl.ID)
		}
	}
	return s.Put(user, DevicePref{DeviceID: deviceID, EnabledRuleIDs: enabled})
}

// Allowed satisfies notify.DevicePrefsResolver: it sweeps mobile-notify-prefs-*.json
// under dataDir looking for deviceID under ANY user. A deviceID never found in
// any file is "unknown" — it fails OPEN (true), never closed — see the docs on
// notify.DevicePrefsResolver for why (so as not to silence every pre-existing
// webpush subscription, which never gets a line here).
func (s *DevicePrefsStore) Allowed(deviceID, ruleID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	matches, _ := filepath.Glob(filepath.Join(strings.TrimRight(s.dataDir, "/"), "mobile-notify-prefs-*.json"))
	sort.Strings(matches)
	for _, path := range matches {
		file, err := loadDevicePrefsFile(path)
		if err != nil || file == nil {
			continue
		}
		for _, d := range file.Devices {
			if d.DeviceID == deviceID {
				return d.has(ruleID)
			}
		}
	}
	return true // unknown everywhere: fails open
}

// ── HTTP ─────────────────────────────────────────────────────────────────────

// ruleSummary is the read-only projection of the Rule catalogue for the
// preferences screen — it reuses notify.Rule's fields (rather than duplicating
// them), plus the computed boolean "enabled_for_device".
type ruleSummary struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	TypePrefix       string `json:"type_prefix,omitempty"`
	MinSeverity      string `json:"min_severity,omitempty"`
	EnabledForDevice bool   `json:"enabled_for_device"`
}

type getNotifyPrefsInput struct {
	DeviceID string `query:"device_id"`
}

type getNotifyPrefsOutput struct {
	Body struct {
		Rules []ruleSummary `json:"rules"`
	}
}

type putNotifyPrefsInput struct {
	Body struct {
		DeviceID       string   `json:"device_id"`
		EnabledRuleIDs []string `json:"enabled_rule_ids"`
	}
}

type putNotifyPrefsOutput struct {
	Body struct {
		Status string `json:"status"`
	}
}

func init() { Register("notify_prefs", registerNotifyPrefs) }

func registerNotifyPrefs(api huma.API, deps Deps) {
	var prefs *DevicePrefsStore
	if deps.Cfg != nil {
		prefs = NewDevicePrefsStore(deps.Cfg.DataDir)
	}
	router := deps.Notify

	huma.Register(api, huma.Operation{
		OperationID: "getNotifyPreferences",
		Method:      http.MethodGet,
		Path:        "/notify/preferences",
		Summary:     "Rules catalog plus what this device receives today",
		Description: "Default (no saved preference): only Rules with critical minimum severity arrive by push; the rest stays in the app inbox.",
		Tags:        []string{"mobile", "notify"},
		Middlewares: huma.Middlewares{requireAuth},
		Errors:      []int{http.StatusUnauthorized, http.StatusServiceUnavailable},
	}, func(ctx context.Context, in *getNotifyPrefsInput) (*getNotifyPrefsOutput, error) {
		user := auth.UserFromContext(ctx)
		if router == nil {
			return nil, huma.Error503ServiceUnavailable("rule catalog unavailable")
		}
		var pref DevicePref
		var hasStored bool
		if prefs != nil && in.DeviceID != "" {
			p, ok, err := prefs.Get(user, in.DeviceID)
			if err != nil {
				return nil, huma.Error500InternalServerError("failed to read the preferences", err)
			}
			pref, hasStored = p, ok
		}
		out := &getNotifyPrefsOutput{}
		for _, rl := range router.Rules() {
			enabled := rl.MinSeverity == notify.SeverityCritical // alert-fatigue default
			if hasStored {
				enabled = pref.has(rl.ID)
			}
			out.Body.Rules = append(out.Body.Rules, ruleSummary{
				ID:               rl.ID,
				Name:             rl.Name,
				TypePrefix:       rl.TypePrefix,
				MinSeverity:      rl.MinSeverity,
				EnabledForDevice: enabled,
			})
		}
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "putNotifyPreferences",
		Method:      http.MethodPut,
		Path:        "/notify/preferences",
		Summary:     "Sets which Rules this device receives by push",
		Tags:        []string{"mobile", "notify"},
		Middlewares: huma.Middlewares{requireAuth},
		Errors:      []int{http.StatusUnauthorized, http.StatusServiceUnavailable},
	}, func(ctx context.Context, in *putNotifyPrefsInput) (*putNotifyPrefsOutput, error) {
		user := auth.UserFromContext(ctx)
		if prefs == nil {
			return nil, huma.Error503ServiceUnavailable("preference storage unavailable")
		}
		if strings.TrimSpace(in.Body.DeviceID) == "" {
			return nil, huma.Error422UnprocessableEntity("device_id is required")
		}
		if err := prefs.Put(user, DevicePref{
			DeviceID:       in.Body.DeviceID,
			EnabledRuleIDs: in.Body.EnabledRuleIDs,
		}); err != nil {
			return nil, huma.Error500InternalServerError("failed to save the preferences", err)
		}
		out := &putNotifyPrefsOutput{}
		out.Body.Status = "ok"
		return out, nil
	})
}
