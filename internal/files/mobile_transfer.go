package files

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

var (
	ErrSessionNotFound = errors.New("upload session not found")
	ErrInvalidChunk    = errors.New("invalid chunk offset/size")
	ErrIncomplete      = errors.New("upload incomplete")
)

const maxUploadSize = 2 << 30

const (
	stagingDirName = ".mobile-upload-staging"
	inboxDirName   = "mobile-inbox"
)

const staleUploadSessionTTL = 24 * time.Hour

type chunkRange struct {
	Offset int64 `json:"offset"`
	Length int64 `json:"length"`
}

type UploadSession struct {
	ID            string       `json:"id"`
	DestDir       string       `json:"dest_dir"`
	Filename      string       `json:"filename"`
	TotalSize     int64        `json:"total_size"`
	ReceivedBytes int64        `json:"received_bytes"`
	Ranges        []chunkRange `json:"ranges"`
	UpdatedAt     int64        `json:"updated_at"`
}

var sessionLocks sync.Map

func sessionLockFor(id string) *sync.Mutex {
	v, _ := sessionLocks.LoadOrStore(id, &sync.Mutex{})
	return v.(*sync.Mutex)
}

func stagingDir(dataDir, sessionID string) string {
	return filepath.Join(dataDir, stagingDirName, sessionID)
}

func sessionSidecarFile(dataDir, sessionID string) string {
	return filepath.Join(stagingDir(dataDir, sessionID), "session.json")
}

func stagingDataFile(dataDir, sessionID string) string {
	return filepath.Join(stagingDir(dataDir, sessionID), "data")
}

func loadSession(dataDir, sessionID string) (UploadSession, error) {
	b, err := os.ReadFile(sessionSidecarFile(dataDir, sessionID))
	if err != nil {
		if os.IsNotExist(err) {
			return UploadSession{}, ErrSessionNotFound
		}
		return UploadSession{}, err
	}
	var s UploadSession
	if err := json.Unmarshal(b, &s); err != nil {
		return UploadSession{}, err
	}
	return s, nil
}

func saveSession(dataDir string, s UploadSession) error {
	s.UpdatedAt = time.Now().Unix()
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	dir := stagingDir(dataDir, s.ID)
	tmp := filepath.Join(dir, "session.json.tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, sessionSidecarFile(dataDir, s.ID)); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func addRange(ranges []chunkRange, offset, length int64) []chunkRange {
	if length <= 0 {
		return ranges
	}
	ranges = append(ranges, chunkRange{Offset: offset, Length: length})
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].Offset < ranges[j].Offset })
	merged := make([]chunkRange, 0, len(ranges))
	for _, r := range ranges {
		if n := len(merged); n > 0 && r.Offset <= merged[n-1].Offset+merged[n-1].Length {
			if end := r.Offset + r.Length; end > merged[n-1].Offset+merged[n-1].Length {
				merged[n-1].Length = end - merged[n-1].Offset
			}
			continue
		}
		merged = append(merged, r)
	}
	return merged
}

func sumRanges(ranges []chunkRange) int64 {
	var total int64
	for _, r := range ranges {
		total += r.Length
	}
	return total
}

func OpenForRange(path string) (*os.File, os.FileInfo, error) {
	real, err := resolveReal(path)
	if err != nil {
		return nil, nil, err
	}
	fi, err := os.Stat(real)
	if err != nil {
		return nil, nil, err
	}
	if fi.IsDir() {
		return nil, nil, fmt.Errorf("is a directory")
	}
	f, err := os.Open(real)
	if err != nil {
		return nil, nil, err
	}
	return f, fi, nil
}

func MobileInboxDir(dataDir string) (string, error) {
	dir := filepath.Join(dataDir, inboxDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func InitUpload(dataDir, destDir, filename string, totalSize int64) (string, error) {
	realDestDir, err := resolveReal(destDir)
	if err != nil {
		return "", err
	}
	destDir = realDestDir
	fi, err := os.Stat(destDir)
	if err != nil {
		return "", err
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("dest_dir is not a directory")
	}
	base := filepath.Base(filename)
	if filename == "" || base == "." || base == "/" || base != filename || strings.Contains(filename, "..") {
		return "", fmt.Errorf("invalid filename")
	}
	if totalSize <= 0 || totalSize > maxUploadSize {
		return "", ErrTooLarge
	}

	sessionID := uuid.NewString()
	dir := stagingDir(dataDir, sessionID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	f, err := os.OpenFile(stagingDataFile(dataDir, sessionID), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	if err := f.Truncate(totalSize); err != nil {
		_ = f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}

	s := UploadSession{ID: sessionID, DestDir: destDir, Filename: filename, TotalSize: totalSize}
	if err := saveSession(dataDir, s); err != nil {
		return "", err
	}
	return sessionID, nil
}

func WriteChunk(dataDir, sessionID string, offset int64, data []byte) (int64, error) {
	mu := sessionLockFor(sessionID)
	mu.Lock()
	defer mu.Unlock()

	s, err := loadSession(dataDir, sessionID)
	if err != nil {
		return 0, err
	}
	if offset < 0 || offset+int64(len(data)) > s.TotalSize {
		return 0, ErrInvalidChunk
	}
	if len(data) == 0 {
		return s.ReceivedBytes, nil
	}

	f, err := os.OpenFile(stagingDataFile(dataDir, sessionID), os.O_WRONLY, 0o600)
	if err != nil {
		return 0, err
	}
	_, werr := f.WriteAt(data, offset)
	cerr := f.Close()
	if werr != nil {
		return 0, werr
	}
	if cerr != nil {
		return 0, cerr
	}

	s.Ranges = addRange(s.Ranges, offset, int64(len(data)))
	s.ReceivedBytes = sumRanges(s.Ranges)
	if err := saveSession(dataDir, s); err != nil {
		return 0, err
	}
	return s.ReceivedBytes, nil
}

func SessionStatus(dataDir, sessionID string) (receivedBytes, totalSize int64, err error) {
	s, err := loadSession(dataDir, sessionID)
	if err != nil {
		return 0, 0, err
	}
	return s.ReceivedBytes, s.TotalSize, nil
}

func CompleteUpload(dataDir, sessionID string) (string, error) {
	mu := sessionLockFor(sessionID)
	mu.Lock()
	defer mu.Unlock()

	s, err := loadSession(dataDir, sessionID)
	if err != nil {
		return "", err
	}
	if s.ReceivedBytes != s.TotalSize {
		return "", ErrIncomplete
	}

	realDestDir, err := resolveReal(s.DestDir)
	if err != nil {
		return "", err
	}
	final := filepath.Join(realDestDir, s.Filename)
	if err := os.Rename(stagingDataFile(dataDir, sessionID), final); err != nil {
		return "", err
	}
	_ = os.RemoveAll(stagingDir(dataDir, sessionID))
	return final, nil
}

func ReapStaleUploadSessions(dataDir string, now time.Time) (removed int, err error) {
	root := filepath.Join(dataDir, stagingDirName)
	entries, readErr := os.ReadDir(root)
	if readErr != nil {
		if os.IsNotExist(readErr) {
			return 0, nil
		}
		return 0, readErr
	}

	var errs []string
	cutoff := now.Add(-staleUploadSessionTTL)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		sessionID := entry.Name()
		if didRemove, rErr := reapOneSessionIfStale(dataDir, sessionID, cutoff); rErr != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", sessionID, rErr))
		} else if didRemove {
			removed++
		}
	}
	if len(errs) > 0 {
		return removed, fmt.Errorf("failed to scan %d session(s): %s", len(errs), strings.Join(errs, "; "))
	}
	return removed, nil
}

func reapOneSessionIfStale(dataDir, sessionID string, cutoff time.Time) (removed bool, err error) {
	mu := sessionLockFor(sessionID)
	mu.Lock()
	defer mu.Unlock()

	lastActivity, ok := sessionLastActivity(dataDir, sessionID)
	if !ok {
		return false, nil
	}
	if lastActivity.After(cutoff) {
		return false, nil
	}
	if err := os.RemoveAll(stagingDir(dataDir, sessionID)); err != nil {
		return false, err
	}
	return true, nil
}

func sessionLastActivity(dataDir, sessionID string) (time.Time, bool) {
	if s, err := loadSession(dataDir, sessionID); err == nil {
		return time.Unix(s.UpdatedAt, 0), true
	}
	fi, err := os.Stat(stagingDir(dataDir, sessionID))
	if err != nil {
		return time.Time{}, false
	}
	return fi.ModTime(), true
}
