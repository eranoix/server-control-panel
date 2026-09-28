package videocall

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"server-control-panel/internal/auth"
)

type RingDevice struct {
	User      string `json:"user"`
	DeviceID  string `json:"device_id"`
	Label     string `json:"label"`
	Ring      bool   `json:"ring"`
	MuteUntil int64  `json:"mute_until,omitempty"`
	LastSeen  int64  `json:"last_seen"`
	CreatedAt int64  `json:"created_at"`
}

func (d *RingDevice) Muted(now int64) bool {
	if d == nil {
		return false
	}
	if !d.Ring {
		return true
	}
	return d.MuteUntil > now
}

const (
	maxDevicesPerUser = 50
	deviceStaleDays   = 120
)

type deviceStore struct {
	mu      sync.RWMutex
	devices map[string]*RingDevice
	path    string
}

func deviceKey(user, deviceID string) string { return user + "\x00" + deviceID }

func newDeviceStore(path string) *deviceStore {
	d := &deviceStore{devices: make(map[string]*RingDevice), path: path}
	_ = d.load()
	return d
}

func (s *deviceStore) load() error {
	b, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var arr []*RingDevice
	if err := json.Unmarshal(b, &arr); err != nil {
		return nil
	}
	cutoff := time.Now().Unix() - int64(deviceStaleDays)*86400
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range arr {
		if d == nil || d.User == "" || d.DeviceID == "" {
			continue
		}
		if d.LastSeen < cutoff {
			continue
		}
		s.devices[deviceKey(d.User, d.DeviceID)] = d
	}
	return nil
}

func (s *deviceStore) save() error {
	s.mu.RLock()
	arr := make([]*RingDevice, 0, len(s.devices))
	for _, d := range s.devices {
		arr = append(arr, d)
	}
	s.mu.RUnlock()
	sort.Slice(arr, func(i, j int) bool {
		if arr[i].User != arr[j].User {
			return arr[i].User < arr[j].User
		}
		return arr[i].DeviceID < arr[j].DeviceID
	})
	return atomicWriteJSON(s.path, arr, 0o600)
}

func (s *deviceStore) Seen(user, deviceID, label string) {
	if s == nil || user == "" || deviceID == "" {
		return
	}
	now := time.Now().Unix()
	s.mu.Lock()
	k := deviceKey(user, deviceID)
	d, ok := s.devices[k]
	if !ok {
		d = &RingDevice{User: user, DeviceID: deviceID, Ring: true, CreatedAt: now}
		s.devices[k] = d
	}
	if label != "" {
		d.Label = label
	}
	d.LastSeen = now
	s.pruneLocked(user)
	s.mu.Unlock()
	_ = s.save()
}

func (s *deviceStore) pruneLocked(user string) {
	var mine []*RingDevice
	for _, d := range s.devices {
		if d.User == user {
			mine = append(mine, d)
		}
	}
	if len(mine) <= maxDevicesPerUser {
		return
	}
	sort.Slice(mine, func(i, j int) bool { return mine[i].LastSeen > mine[j].LastSeen })
	for _, d := range mine[maxDevicesPerUser:] {
		delete(s.devices, deviceKey(d.User, d.DeviceID))
	}
}

func (s *deviceStore) ShouldRing(user, deviceID string, now int64) bool {
	if s == nil || user == "" || deviceID == "" {
		return true
	}
	s.mu.RLock()
	d, ok := s.devices[deviceKey(user, deviceID)]
	s.mu.RUnlock()
	if !ok {
		return true
	}
	return !d.Muted(now)
}

func (s *deviceStore) List(user string) []RingDevice {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]RingDevice, 0, 4)
	for _, d := range s.devices {
		if d.User == user {
			out = append(out, *d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastSeen > out[j].LastSeen })
	return out
}

func (s *deviceStore) Set(user, deviceID string, ring *bool, muteUntil *int64, label string) bool {
	if s == nil || user == "" || deviceID == "" {
		return false
	}
	s.mu.Lock()
	d, ok := s.devices[deviceKey(user, deviceID)]
	if !ok {
		s.mu.Unlock()
		return false
	}
	if ring != nil {
		d.Ring = *ring
		if *ring {
			d.MuteUntil = 0
		}
	}
	if muteUntil != nil {
		d.MuteUntil = *muteUntil
	}
	if label != "" {
		d.Label = label
	}
	s.mu.Unlock()
	_ = s.save()
	return true
}

func (s *deviceStore) Forget(user, deviceID string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	k := deviceKey(user, deviceID)
	_, ok := s.devices[k]
	if ok {
		delete(s.devices, k)
	}
	s.mu.Unlock()
	if ok {
		_ = s.save()
	}
	return ok
}

func sanitizeDeviceID(s string) string {
	if len(s) > 64 {
		s = s[:64]
	}
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			out = append(out, c)
		}
	}
	return string(out)
}

func sanitizeDeviceLabel(s string) string {
	runes := []rune(strings.TrimSpace(s))
	if len(runes) > 60 {
		runes = runes[:60]
	}
	out := make([]rune, 0, len(runes))
	for _, c := range runes {
		if c < 0x20 || c == 0x7f {
			continue
		}
		out = append(out, c)
	}
	return strings.TrimSpace(string(out))
}

func (s *Service) HandleDevices(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFrom(r)
	if user == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if s.Devices == nil {
		http.Error(w, "devices unavailable", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSONHTTP(w, map[string]any{
			"devices": s.Devices.List(user),
			"now":     time.Now().Unix(),
		})
	case http.MethodPost:
		var body struct {
			DeviceID  string `json:"device_id"`
			Ring      *bool  `json:"ring"`
			MuteHours *int   `json:"mute_hours"`
			Label     string `json:"label"`
			Forget    bool   `json:"forget"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		id := sanitizeDeviceID(body.DeviceID)
		if id == "" {
			http.Error(w, "missing device_id", http.StatusBadRequest)
			return
		}
		if body.Forget {
			if !s.Devices.Forget(user, id) {
				http.Error(w, "device not found", http.StatusNotFound)
				return
			}
			s.audit("videocall.device_forget", user, id)
			writeJSONHTTP(w, map[string]any{"ok": true})
			return
		}
		var muteUntil *int64
		if body.MuteHours != nil {
			h := *body.MuteHours
			if h < 0 {
				h = 0
			}
			if h > 720 {
				h = 720
			}
			var v int64
			if h > 0 {
				v = time.Now().Unix() + int64(h)*3600
			}
			muteUntil = &v
		}
		if !s.Devices.Set(user, id, body.Ring, muteUntil, sanitizeDeviceLabel(body.Label)) {
			http.Error(w, "device not found", http.StatusNotFound)
			return
		}
		s.audit("videocall.device_policy", user, id)
		writeJSONHTTP(w, map[string]any{"ok": true, "devices": s.Devices.List(user)})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
