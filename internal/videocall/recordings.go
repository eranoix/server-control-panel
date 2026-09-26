package videocall

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/httpmw"
)

// recordingStore keeps the in-memory index of the cloud recordings.
// The files live in /var/lib/vpsm-videocalls/recordings/<user>/<id>.webm
// and the index in data/videocalls/recordings.json.
//
// Deliberate decision: do NOT use S3/MinIO in the MVP. The local server already
// has disk and the use case is domestic (a call with my wife). Migrating to S3
// later means swapping saveFile() and the reader of the GET route; the index
// (with a hash for integrity + size + owner) already has all an S3 backend needs.
type recordingStore struct {
	mu     sync.RWMutex
	items  map[string]*Recording // id → record
	path   string                // index file: data/videocalls/recordings.json
	dirty  bool
	rootFS string // /var/lib/vpsm-videocalls/recordings
	stop   chan struct{}
}

// Close signals the flusher to stop and does a final flush.
func (s *recordingStore) Close() error {
	if s == nil {
		return nil
	}
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
	return s.save()
}

// Recording is the metadata persisted in the index. The blob lives in a
// separate file so the JSON does not bloat.
type Recording struct {
	ID         string `json:"id"`
	User       string `json:"user"`
	RoomID     string `json:"room_id"`
	RoomName   string `json:"room_name,omitempty"`
	StartedAt  int64  `json:"started_at"`
	DurationS  int64  `json:"duration_s"`
	SizeBytes  int64  `json:"size_bytes"`
	MimeType   string `json:"mime_type"`
	CreatedAt  int64  `json:"created_at"`
	HasSummary bool   `json:"has_summary,omitempty"` // AI summary available
}

const (
	// 500MB hard limit per recording — enough for ~30min of VP9 1080p.
	// Bigger than that becomes a storage problem and the browser cannot hold it.
	recordingMaxBytes = 500 << 20
	// Total per user (rolling) — stops a single user from filling the disk.
	// Once past it, the oldest one is removed.
	recordingMaxPerUser = 20
)

func init() {
	// httpmw.MaxBody applies 25 MiB by default on every route. HandleRecordingUpload
	// already rewrapped r.Body in a MaxBytesReader of its own at
	// recordingMaxBytes+1MiB (the line below, in HandleRecordingUpload) — but that
	// never had any effect: MaxBytesReader does not loosen a smaller limit already
	// applied by an earlier wrapper on the same r.Body, and the global one (25 MiB)
	// always ran first in the middleware chain. Every video recording above 25 MiB
	// — that is, practically any call longer than a few seconds — was being cut off
	// before reaching here. RegisterLargeBody is the only way to widen the ceiling
	// BEFORE the request reaches the handler.
	httpmw.RegisterLargeBody(isRecordingUpload, recordingMaxBytes+1<<20)
}

// isRecordingUpload matches exactly POST /api/videocall/recordings — the only
// route in the recordings family that receives a large blob (the others are
// GET/DELETE of metadata, or /summarize, which has no relevant body).
func isRecordingUpload(r *http.Request) bool {
	return r.Method == http.MethodPost && r.URL.Path == "/api/videocall/recordings"
}

// OpenRecordingStore is the exported entry point used by api.NewRouter.
// It aliases the internal implementation to keep the other helpers private.
func OpenRecordingStore(dataDir, blobsRoot string) (*recordingStore, error) {
	return openRecordingStore(dataDir, blobsRoot)
}

func openRecordingStore(dataDir, blobsRoot string) (*recordingStore, error) {
	if err := os.MkdirAll(blobsRoot, 0o700); err != nil {
		return nil, fmt.Errorf("mkdir blobsRoot: %w", err)
	}
	root := filepath.Join(dataDir, "videocalls")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	s := &recordingStore{
		items:  make(map[string]*Recording),
		path:   filepath.Join(root, "recordings.json"),
		rootFS: blobsRoot,
		stop:   make(chan struct{}),
	}
	_ = s.load()
	go s.flusher()
	return s, nil
}

func (s *recordingStore) load() error {
	b, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var arr []*Recording
	if err := json.Unmarshal(b, &arr); err != nil {
		return nil // tolerant
	}
	for _, r := range arr {
		if r.ID != "" {
			s.items[r.ID] = r
		}
	}
	return nil
}

func (s *recordingStore) flusher() {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			s.mu.Lock()
			need := s.dirty
			s.dirty = false
			s.mu.Unlock()
			if need {
				_ = s.save()
			}
		}
	}
}

func (s *recordingStore) save() error {
	s.mu.RLock()
	arr := make([]*Recording, 0, len(s.items))
	for _, r := range s.items {
		arr = append(arr, r)
	}
	s.mu.RUnlock()
	sort.Slice(arr, func(i, j int) bool { return arr[i].CreatedAt < arr[j].CreatedAt })
	return atomicWriteJSON(s.path, arr, 0o600)
}

func (s *recordingStore) blobPath(user, id string) string {
	return filepath.Join(s.rootFS, sanitizeFS(user), id+".webm")
}

// Add registers the metadata and writes the file. If it exceeds the per-user
// limit, the oldest one is dropped (FIFO).
func (s *recordingStore) Add(rec *Recording, src io.Reader, hardLimitBytes int64) error {
	if rec.User == "" {
		return errors.New("missing user")
	}
	if rec.ID == "" {
		rec.ID = randHexID(12)
	}
	if rec.CreatedAt == 0 {
		rec.CreatedAt = time.Now().Unix()
	}
	dst := s.blobPath(rec.User, rec.ID)
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(dst+".tmp", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	limited := io.LimitReader(src, hardLimitBytes+1)
	n, err := io.Copy(f, limited)
	_ = f.Close()
	if err != nil {
		_ = os.Remove(dst + ".tmp")
		return err
	}
	if n > hardLimitBytes {
		_ = os.Remove(dst + ".tmp")
		return fmt.Errorf("recording exceeds %d bytes", hardLimitBytes)
	}
	if err := os.Rename(dst+".tmp", dst); err != nil {
		_ = os.Remove(dst + ".tmp")
		return err
	}
	rec.SizeBytes = n
	s.mu.Lock()
	s.items[rec.ID] = rec
	s.dirty = true
	s.mu.Unlock()
	// FIFO trim per user.
	s.trimUser(rec.User)
	return nil
}

func (s *recordingStore) trimUser(user string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var owned []*Recording
	for _, r := range s.items {
		if r.User == user {
			owned = append(owned, r)
		}
	}
	if len(owned) <= recordingMaxPerUser {
		return
	}
	sort.Slice(owned, func(i, j int) bool { return owned[i].CreatedAt < owned[j].CreatedAt })
	excess := len(owned) - recordingMaxPerUser
	for i := 0; i < excess; i++ {
		old := owned[i]
		_ = os.Remove(s.blobPath(old.User, old.ID))
		delete(s.items, old.ID)
		_ = os.Remove(s.blobPath(old.User, old.ID) + ".summary.txt")
	}
	s.dirty = true
}

// Get returns a copy of the metadata (or false if it does not exist / is not owned).
func (s *recordingStore) Get(user, id string) (Recording, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.items[id]
	if !ok || r.User != user {
		return Recording{}, false
	}
	return *r, true
}

// ForUser lists the user's recordings, newest first.
func (s *recordingStore) ForUser(user string) []Recording {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Recording, 0)
	for _, r := range s.items {
		if r.User == user {
			out = append(out, *r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out
}

// Delete remove blob + entry. Owner-only.
func (s *recordingStore) Delete(user, id string) error {
	s.mu.Lock()
	r, ok := s.items[id]
	if !ok || r.User != user {
		s.mu.Unlock()
		return errors.New("not found")
	}
	delete(s.items, id)
	s.dirty = true
	s.mu.Unlock()
	_ = os.Remove(s.blobPath(user, id))
	_ = os.Remove(s.blobPath(user, id) + ".summary.txt")
	return nil
}

// SetSummary persists the AI summary for a recording. Called by
// HandleSummarize. The HasSummary index field becomes true.
func (s *recordingStore) SetSummary(user, id, summary string) error {
	s.mu.RLock()
	r, ok := s.items[id]
	if !ok || r.User != user {
		s.mu.RUnlock()
		return errors.New("not found")
	}
	s.mu.RUnlock()
	if err := os.WriteFile(s.blobPath(user, id)+".summary.txt", []byte(summary), 0o600); err != nil {
		return err
	}
	s.mu.Lock()
	r.HasSummary = true
	s.dirty = true
	s.mu.Unlock()
	return nil
}

func (s *recordingStore) Summary(user, id string) (string, error) {
	r, ok := s.Get(user, id)
	if !ok {
		return "", errors.New("not found")
	}
	if !r.HasSummary {
		return "", errors.New("no summary yet")
	}
	b, err := os.ReadFile(s.blobPath(user, id) + ".summary.txt")
	return string(b), err
}

// --- HTTP handlers --------------------------------------------------

// HandleRecordingsRouter dispatch:
//
//	POST /api/videocall/recordings → upload
//	GET  /api/videocall/recordings → list
func (s *Service) HandleRecordingsRouter(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.HandleRecordingList(w, r)
	case http.MethodPost:
		s.HandleRecordingUpload(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// HandleRecordingItemRouter dispatch by trailing segment of the path:
//
//	GET    /api/videocall/recordings/{id}              → metadata
//	GET    /api/videocall/recordings/{id}/blob         → bytes
//	GET    /api/videocall/recordings/{id}/summary      → cached summary
//	POST   /api/videocall/recordings/{id}/summarize    → generate summary
//	DELETE /api/videocall/recordings/{id}              → delete
func (s *Service) HandleRecordingItemRouter(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/summarize") {
		s.HandleSummarize(w, r)
		return
	}
	s.HandleRecordingItem(w, r)
}

// HandleRecordingUpload is multipart: a "file" field with the blob plus the form
// fields room_id, started_at, duration_s. Size limited to recordingMaxBytes.
func (s *Service) HandleRecordingUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user := auth.UserFrom(r)
	if user == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if s.Recordings == nil {
		http.Error(w, "recordings disabled", http.StatusServiceUnavailable)
		return
	}
	// Limit the request body up front so a malicious sender can't OOM us.
	r.Body = http.MaxBytesReader(w, r.Body, recordingMaxBytes+1<<20)
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		http.Error(w, "bad form: "+err.Error(), http.StatusBadRequest)
		return
	}
	roomID := r.FormValue("room_id")
	startedAt, _ := parseInt64(r.FormValue("started_at"))
	durationS, _ := parseInt64(r.FormValue("duration_s"))
	if roomID == "" {
		http.Error(w, "missing room_id", http.StatusBadRequest)
		return
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing file: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer f.Close()
	// Authz: the user has to be owner OR member of the room. Without this, any
	// logged-in user could pollute the index by referencing someone else's
	// room_id. RoomForUser returns 404 for a non-member (existence is not leaked).
	room, ok := s.RoomForUser(user, roomID)
	if !ok {
		http.Error(w, "room not found or access denied", http.StatusNotFound)
		return
	}
	// Sanity check: clamp durations/timestamps to realistic ranges to avoid a
	// corrupt index (found during the audit — it used to accept negatives).
	if startedAt < 0 || startedAt > time.Now().Unix()+3600 {
		startedAt = 0 // fallback: RecordCallSession-like default
	}
	if durationS < 0 || durationS > 24*3600 {
		durationS = 0
	}
	rec := &Recording{
		User:      user,
		RoomID:    roomID,
		RoomName:  room.Name,
		StartedAt: startedAt,
		DurationS: durationS,
		MimeType:  hdr.Header.Get("Content-Type"),
	}
	if err := s.Recordings.Add(rec, f, recordingMaxBytes); err != nil {
		http.Error(w, "save: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.audit("videocall.recording_uploaded", user, roomID)
	writeJSONHTTP(w, rec)
}

// HandleRecordingList — GET /api/videocall/recordings
func (s *Service) HandleRecordingList(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFrom(r)
	if user == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if s.Recordings == nil {
		writeJSONHTTP(w, []Recording{})
		return
	}
	writeJSONHTTP(w, s.Recordings.ForUser(user))
}

// HandleRecordingGet — GET /api/videocall/recordings/{id}/blob → bytes
// HandleRecordingMeta — GET /api/videocall/recordings/{id}        → metadata
// We dispatch on the trailing path.
func (s *Service) HandleRecordingItem(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFrom(r)
	if user == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if s.Recordings == nil {
		http.Error(w, "disabled", http.StatusServiceUnavailable)
		return
	}
	// Path: /api/videocall/recordings/{id}[/blob|/summary]
	rest := strings.TrimPrefix(r.URL.Path, "/api/videocall/recordings/")
	parts := strings.SplitN(rest, "/", 2)
	id := parts[0]
	tail := ""
	if len(parts) > 1 {
		tail = parts[1]
	}
	switch r.Method {
	case http.MethodGet:
		rec, ok := s.Recordings.Get(user, id)
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		switch tail {
		case "":
			writeJSONHTTP(w, rec)
		case "blob":
			f, err := os.Open(s.Recordings.blobPath(user, id))
			if err != nil {
				http.Error(w, "blob missing", http.StatusGone)
				return
			}
			defer f.Close()
			w.Header().Set("Content-Type", rec.MimeType)
			w.Header().Set("Content-Length", fmt.Sprintf("%d", rec.SizeBytes))
			w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="videocall-%s.webm"`, id))
			_, _ = io.Copy(w, f)
		case "summary":
			sum, err := s.Recordings.Summary(user, id)
			if err != nil {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte(sum))
		default:
			http.NotFound(w, r)
		}
	case http.MethodDelete:
		if err := s.Recordings.Delete(user, id); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		s.audit("videocall.recording_deleted", user, id)
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// --- utilities ------------------------------------------------------

func sanitizeFS(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-' || c == '.' {
			out = append(out, c)
		} else {
			out = append(out, '_')
		}
	}
	if len(out) == 0 {
		return "anon"
	}
	return string(out)
}

func parseInt64(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	var v int64
	_, err := fmt.Sscanf(s, "%d", &v)
	return v, err
}

func randHexID(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
