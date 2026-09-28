package mobilebff

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

type DeviceEntry struct {
	DeviceID  string `json:"device_id"`
	FCMToken  string `json:"fcm_token"`
	Platform  string `json:"platform,omitempty"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

type deviceTokenFile struct {
	SchemaVersion int           `json:"schema_version"`
	User          string        `json:"user"`
	Devices       []DeviceEntry `json:"devices"`
}

type DeviceTokenStore struct {
	mu      sync.Mutex
	dataDir string
}

func NewDeviceTokenStore(dataDir string) *DeviceTokenStore {
	return &DeviceTokenStore{dataDir: dataDir}
}

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

func (s *DeviceTokenStore) List(user string) ([]DeviceEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := loadDeviceTokenFile(DeviceTokenStorePath(s.dataDir, user))
	if err != nil || file == nil {
		return nil, err
	}
	return file.Devices, nil
}

func (s *DeviceTokenStore) TokensForUser(user string) []fcmpush.DeviceToken {
	devices, _ := s.List(user)
	out := make([]fcmpush.DeviceToken, 0, len(devices))
	for _, d := range devices {
		out = append(out, fcmpush.DeviceToken{DeviceID: d.DeviceID, Token: d.FCMToken})
	}
	return out
}

func (s *DeviceTokenStore) AllTokens() []fcmpush.DeviceToken {
	s.mu.Lock()
	defer s.mu.Unlock()
	matches, _ := filepath.Glob(filepath.Join(strings.TrimRight(s.dataDir, "/"), "mobile-devices-*.json"))
	sort.Strings(matches)
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

type registerDeviceInput struct {
	Body struct {
		DeviceID string `json:"device_id" doc:"ID minted by the client (e.g. ANDROID_ID)"`
		FCMToken string `json:"fcm_token"`
		Platform string `json:"platform,omitempty" doc:"android today; open to more in the future"`
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
		Summary:     "Registers (or updates) this device's FCM token",
		Description: "device_id is minted by the app itself (never by the server). Upsert: calling again with the same device_id only updates the token. On the first registration of a new device_id, seeds default preferences (critical events only) in DevicePrefsStore.",
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
		Summary:     "Removes this device's native push registration (e.g. on logout)",
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
