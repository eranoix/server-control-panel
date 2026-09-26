package mobilebff

// push_devices.go — Android device registration for native push (FCM). The
// device_id is MINTED BY THE CLIENT (Settings.Secure.ANDROID_ID-based; the auth
// work was checked and confirmed that MobileRefreshStore exposes no stable
// device identifier at all) — this endpoint only upserts by that id, it never
// generates a new one.
//
// Persistence mirrors internal/auth.TrustedDevicesStore: one JSON file per user
// under DataDir, an in-memory mutex, atomic writes (tmp+rename).
// DeviceTokenStore, unlike TrustedDevicesStore, also has to answer for ALL
// users at once (AllTokens, for broadcasting a Rule with no ToUser) — which is
// why it is built once with the whole DataDir (not a specific file), and scans
// per user on demand.

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
	"server-control-panel/internal/notify/fcmpush"
)

// DeviceEntry is one Android device registered for native push.
type DeviceEntry struct {
	DeviceID  string `json:"device_id"`
	FCMToken  string `json:"fcm_token"`
	Platform  string `json:"platform,omitempty"` // "android" today; open to more in the future
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

type deviceTokenFile struct {
	SchemaVersion int           `json:"schema_version"`
	User          string        `json:"user"`
	Devices       []DeviceEntry `json:"devices"`
}

// DeviceTokenStore persists the devices of ALL users, one JSON file per user
// under dataDir (mobile-devices-<user>.json). Built exactly once (see
// internal/api/notify_wire.go) and satisfies fcmpush.DeviceStore structurally —
// this package never imports internal/notify, and fcmpush never imports this
// package.
type DeviceTokenStore struct {
	mu      sync.Mutex
	dataDir string
}

// NewDeviceTokenStore creates a store pointing at dataDir. It reads nothing —
// reading is lazy, per method, like TrustedDevicesStore.
func NewDeviceTokenStore(dataDir string) *DeviceTokenStore {
	return &DeviceTokenStore{dataDir: dataDir}
}

// DeviceTokenStorePath returns the canonical file path for one user.
func DeviceTokenStorePath(dataDir, user string) string {
	return filepath.Join(strings.TrimRight(dataDir, "/"), fmt.Sprintf("mobile-devices-%s.json", user))
}

func loadDeviceTokenFile(path string) (*deviceTokenFile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("push_devices: read: %w", err)
	}
	var f deviceTokenFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("push_devices: parse: %w", err)
	}
	return &f, nil
}

func saveDeviceTokenFile(path string, f *deviceTokenFile) error {
	if len(f.Devices) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("push_devices: remove empty: %w", err)
		}
		return nil
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("push_devices: marshal: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return fmt.Errorf("push_devices: write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("push_devices: rename: %w", err)
	}
	return nil
}

// Upsert stores (or replaces, by the same device_id) user's device. It returns
// true if the device_id is NEW for this user — the caller (the HTTP handler
// below) uses that to decide whether to seed a default DevicePrefsStore
// (critical-only) the first time the device shows up.
func (s *DeviceTokenStore) Upsert(user string, e DeviceEntry) (isNew bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := DeviceTokenStorePath(s.dataDir, user)
	file, err := loadDeviceTokenFile(path)
	if err != nil {
		return false, err
	}
	if file == nil {
		file = &deviceTokenFile{SchemaVersion: 1, User: user}
	}
	now := time.Now().Unix()
	e.UpdatedAt = now
	isNew = true
	for i := range file.Devices {
		if file.Devices[i].DeviceID == e.DeviceID {
			e.CreatedAt = file.Devices[i].CreatedAt
			file.Devices[i] = e
			isNew = false
			break
		}
	}
	if isNew {
		e.CreatedAt = now
		file.Devices = append(file.Devices, e)
	}
	if err := saveDeviceTokenFile(path, file); err != nil {
		return false, err
	}
	return isNew, nil
}

// Remove deletes user's device deviceID. Idempotent.
func (s *DeviceTokenStore) Remove(user, deviceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := DeviceTokenStorePath(s.dataDir, user)
	file, err := loadDeviceTokenFile(path)
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
	return saveDeviceTokenFile(path, file)
}

// List returns user's registered devices. Missing file → [].
func (s *DeviceTokenStore) List(user string) ([]DeviceEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := loadDeviceTokenFile(DeviceTokenStorePath(s.dataDir, user))
	if err != nil || file == nil {
		return nil, err
	}
	return file.Devices, nil
}

// TokensForUser satisfies fcmpush.DeviceStore: the FCM tokens of a single
// user.
func (s *DeviceTokenStore) TokensForUser(user string) []fcmpush.DeviceToken {
	devices, _ := s.List(user)
	out := make([]fcmpush.DeviceToken, 0, len(devices))
	for _, d := range devices {
		out = append(out, fcmpush.DeviceToken{DeviceID: d.DeviceID, Token: d.FCMToken})
	}
	return out
}

// AllTokens satisfies fcmpush.DeviceStore: the FCM tokens of ALL users —
// needed for a Rule with no ToUser (broadcast). It sweeps
// mobile-devices-*.json under dataDir; a glob, not an in-memory index, because
// this process already tolerates (in other stores following the same pattern)
// rereading from disk on every call rather than keeping a cache that would need
// invalidation of its own.
func (s *DeviceTokenStore) AllTokens() []fcmpush.DeviceToken {
	s.mu.Lock()
	defer s.mu.Unlock()
	matches, _ := filepath.Glob(filepath.Join(strings.TrimRight(s.dataDir, "/"), "mobile-devices-*.json"))
	sort.Strings(matches) // deterministic order (tests, visual dedupe in logs)
	var out []fcmpush.DeviceToken
	for _, path := range matches {
		file, err := loadDeviceTokenFile(path)
		if err != nil || file == nil {
			continue
		}
		for _, d := range file.Devices {
			out = append(out, fcmpush.DeviceToken{DeviceID: d.DeviceID, Token: d.FCMToken})
		}
	}
	return out
}

// ── HTTP ─────────────────────────────────────────────────────────────────────

type registerDeviceInput struct {
	Body struct {
		DeviceID string `json:"device_id" doc:"ID mintado pelo cliente (ex.: ANDROID_ID)"`
		FCMToken string `json:"fcm_token"`
		Platform string `json:"platform,omitempty" doc:"android hoje; aberto a mais no futuro"`
	}
}

type registerDeviceOutput struct {
	Body struct {
		Status string `json:"status"`
	}
}

type unregisterDeviceInput struct {
	DeviceID string `path:"device_id"`
}

type unregisterDeviceOutput struct {
	Body struct {
		Status string `json:"status"`
	}
}

func init() { Register("push_devices", registerPushDevices) }

func registerPushDevices(api huma.API, deps Deps) {
	var store *DeviceTokenStore
	var prefs *DevicePrefsStore
	if deps.Cfg != nil {
		store = NewDeviceTokenStore(deps.Cfg.DataDir)
		prefs = NewDevicePrefsStore(deps.Cfg.DataDir)
	}

	huma.Register(api, huma.Operation{
		OperationID: "registerPushDevice",
		Method:      http.MethodPost,
		Path:        "/notify/devices",
		Summary:     "Registra (ou atualiza) o token FCM deste dispositivo",
		Description: "device_id é mintado pelo próprio app (nunca pelo servidor). Upsert: chamar de novo com o mesmo device_id só atualiza o token. No primeiro registro de um device_id novo, semeia preferências default (apenas eventos críticos) em DevicePrefsStore.",
		Tags:        []string{"mobile", "notify"},
		Middlewares: huma.Middlewares{requireAuth},
		Errors:      []int{http.StatusUnauthorized, http.StatusServiceUnavailable},
	}, func(ctx context.Context, in *registerDeviceInput) (*registerDeviceOutput, error) {
		user := auth.UserFromContext(ctx)
		if store == nil {
			return nil, huma.Error503ServiceUnavailable("device storage unavailable")
		}
		if strings.TrimSpace(in.Body.DeviceID) == "" || strings.TrimSpace(in.Body.FCMToken) == "" {
			return nil, huma.Error422UnprocessableEntity("device_id and fcm_token are required")
		}
		platform := in.Body.Platform
		if platform == "" {
			platform = "android"
		}
		isNew, err := store.Upsert(user, DeviceEntry{
			DeviceID: in.Body.DeviceID,
			FCMToken: in.Body.FCMToken,
			Platform: platform,
		})
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to save the device", err)
		}
		if isNew && prefs != nil {
			// Seed the alert-fatigue default: only critical gets through by
			// push until the user opts into more.
			_ = prefs.SeedDefaults(user, in.Body.DeviceID, deps.Notify)
		}
		out := &registerDeviceOutput{}
		out.Body.Status = "ok"
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "unregisterPushDevice",
		Method:      http.MethodDelete,
		Path:        "/notify/devices/{device_id}",
		Summary:     "Remove o registro de push nativo deste dispositivo (ex.: no logout)",
		Tags:        []string{"mobile", "notify"},
		Middlewares: huma.Middlewares{requireAuth},
		Errors:      []int{http.StatusUnauthorized, http.StatusServiceUnavailable},
	}, func(ctx context.Context, in *unregisterDeviceInput) (*unregisterDeviceOutput, error) {
		user := auth.UserFromContext(ctx)
		if store == nil {
			return nil, huma.Error503ServiceUnavailable("device storage unavailable")
		}
		if err := store.Remove(user, in.DeviceID); err != nil {
			return nil, huma.Error500InternalServerError("failed to remove the device", err)
		}
		if prefs != nil {
			_ = prefs.Remove(user, in.DeviceID)
		}
		out := &unregisterDeviceOutput{}
		out.Body.Status = "ok"
		return out, nil
	})
}
