package files

// mobile_transfer.go covers the transfer of LARGE files to the Android app:
// download with Range support (resumable, with no re-download of everything
// after a dropped connection) and chunked upload, plus the default "inbox"
// directory used by the sharing flow (share target).
//
// Why it exists apart from mobile_adapter.go: that file covers the text
// editor (read/write small files, each fitting entirely in memory, capped at
// 2MB); this one covers files where neither opening everything in memory at
// once (download) nor demanding that the whole upload arrive in a single
// request (upload) is acceptable over a phone connection that drops halfway.
//
// Resume contract: the client (the Android app) is the one that decides, on
// reconnecting after a failure, to re-ask how many bytes were already
// received (received_bytes, via SessionStatus) and to resume sending from
// there. The server never tries to guess which bytes "probably" made it
// across the network — it trusts only what has already been durably written
// to the staging file.

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

// Sentinels of the chunked upload protocol — internal/mobilebff maps each one
// to the corresponding HTTP status without this package needing to know
// anything about HTTP.
var (
	ErrSessionNotFound = errors.New("upload session not found")
	ErrInvalidChunk    = errors.New("invalid chunk offset/size")
	ErrIncomplete      = errors.New("upload incomplete")
)

// maxUploadSize is the ceiling on the total_size accepted by InitUpload:
// without it, a single request could reserve a file of arbitrary size and
// exhaust the disk before a single real byte had arrived.
const maxUploadSize = 2 << 30 // 2 GiB

const (
	stagingDirName = ".mobile-upload-staging"
	inboxDirName   = "mobile-inbox"
)

// staleUploadSessionTTL is the retention window of the reaper for abandoned
// upload sessions. 24h comfortably covers any legitimate resume — an upload
// of up to 2 GiB (maxUploadSize) over a bad phone connection that drops and
// resumes hours later would still have a WriteChunk recent enough not to be
// touched — and it still guarantees that disk held by a genuinely abandoned
// upload (app uninstalled, session cancelled without telling the server) is
// handed back in under a day, not never.
const staleUploadSessionTTL = 24 * time.Hour

// chunkRange is a byte interval already received and durably written to the
// staging file.
type chunkRange struct {
	Offset int64 `json:"offset"`
	Length int64 `json:"length"`
}

// UploadSession is the JSON sidecar of an in-flight upload session, written
// to <dataDir>/.mobile-upload-staging/<ID>/session.json. Ranges keeps MERGED
// intervals (non-overlapping, ordered by offset) rather than merely summing
// the size of each chunk received — that way a re-sent chunk (a client retry
// after a connection that dropped before the ACK) or out-of-order chunks
// never artificially inflate ReceivedBytes, and ReceivedBytes only matches
// TotalSize when the WHOLE interval [0, TotalSize) has genuinely been
// covered — not merely when the sum of the parts received adds up by
// coincidence.
type UploadSession struct {
	ID            string       `json:"id"`
	DestDir       string       `json:"dest_dir"`
	Filename      string       `json:"filename"`
	TotalSize     int64        `json:"total_size"`
	ReceivedBytes int64        `json:"received_bytes"`
	Ranges        []chunkRange `json:"ranges"`
	// UpdatedAt (unix seconds) is stamped on every saveSession — InitUpload
	// (creation) and each WriteChunk (progress). It is the only activity signal
	// ReapStaleUploadSessions uses: an "in-flight" session is, by definition,
	// one whose UpdatedAt is recent — no matter how many bytes it has already
	// received.
	UpdatedAt int64 `json:"updated_at"`
}

// sessionLocks serializes write and read-modify per session (not globally) —
// the same pattern as writeLocks in mobile_adapter.go, only keyed by
// session_id instead of by file path.
var sessionLocks sync.Map // map[string]*sync.Mutex

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

// saveSession writes the sidecar atomically (temporary file in the same
// directory + rename), the same pattern as MobileWrite — a crash mid-write
// never leaves the sidecar corrupted/truncated, which would throw away the
// progress of an upload that already had real bytes written to disk.
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

// addRange inserts [offset, offset+length) into the list of received
// intervals and merges it with overlapping/adjacent neighbours, keeping the
// list always ordered and free of overlap.
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

// OpenForRange opens `path` for reading, ready to be used with
// http.ServeContent (which handles Range/If-Modified-Since on its own — no
// Range parsing is reimplemented here). It reuses the very same path
// validation as the file editor (resolveReal, with the symlink check):
// no second denylist is created for download.
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

// MobileInboxDir returns (creating it if needed) the default directory where
// a file shared from another Android app (share target) lands when the user
// picks no explicit destination. It sits under dataDir (the panel's own
// Config.DataDir), on the SAME filesystem used for upload staging — the
// condition CompleteUpload needs in order to move (atomic os.Rename) instead
// of copying.
func MobileInboxDir(dataDir string) (string, error) {
	dir := filepath.Join(dataDir, inboxDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// InitUpload validates the destination and the file name, reserves a staging
// file of the final size (sparse: Truncate does not really write the zeros on
// filesystems that support sparse files, so reserving consumes neither real
// memory nor real disk beyond the metadata) and returns a fresh session_id.
// The staging file lives under
// <dataDir>/.mobile-upload-staging/<session_id>/ — on the SAME filesystem as
// dataDir, so that CompleteUpload can move it (atomic rename) to the final
// destination without copying the bytes all over again.
func InitUpload(dataDir, destDir, filename string, totalSize int64) (string, error) {
	// resolveReal (the same function OpenForRange uses for download and that
	// mobile_adapter.go uses for list/read/write): it validates the raw string,
	// resolves symlinks and validates the resolved path AGAIN. Without it, a
	// symlink inside an allowed directory, pointing outside of it
	// (e.g. /root/.ssh or data/secrets), would pass the string check and
	// would only reveal its true target at CompleteUpload's os.Rename — far
	// too late. The RESOLVED path (not the raw one) is what gets persisted into
	// the session just below, so that CompleteUpload never has to trust an
	// as-yet-unresolved destDir again.
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
	// The same guard as handleUpload (files.go): the name must not escape the
	// destination chosen in InitUpload through ".." or a path separator.
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

// WriteChunk writes `data` at position `offset` of the staging file of
// session `sessionID`. The write is positional (WriteAt), never an append —
// that is what lets out-of-order chunks (a resume) assemble the right file:
// each chunk knows exactly where it goes, regardless of the order it arrived
// in over the network.
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

// SessionStatus returns a session's current progress without changing
// anything. The HTTP handler uses it to build the 409 body of
// upload/complete when the session is still incomplete, so the app knows
// exactly how much is missing without a second call.
func SessionStatus(dataDir, sessionID string) (receivedBytes, totalSize int64, err error) {
	s, err := loadSession(dataDir, sessionID)
	if err != nil {
		return 0, 0, err
	}
	return s.ReceivedBytes, s.TotalSize, nil
}

// CompleteUpload finalizes the session: if bytes are still missing it returns
// ErrIncomplete and touches NOTHING — the session stays open and resumable,
// the client only has to send the chunks that are missing (SessionStatus says
// which). If it is complete, it moves (atomic rename, same filesystem) the
// staging file to dest_dir/filename and removes the session's staging directory.
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

	// Resolve again rather than merely trusting the already-resolved DestDir
	// written by InitUpload: the session sits on disk (session.json) for up to
	// staleUploadSessionTTL between the init and this complete, and the
	// filesystem can change in that interval — someone swapping the destination
	// directory for a symlink after InitUpload validated the original is exactly
	// the kind of TOCTOU that this second check closes, at the same cost (one
	// stat/EvalSymlinks) we already pay on every editor read/write.
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

// ReapStaleUploadSessions sweeps <dataDir>/.mobile-upload-staging and removes
// session directories whose last activity (UpdatedAt) is older than
// staleUploadSessionTTL. Meant to run periodically through a scheduled
// queue.Runner (see MobileUploadStagingReapRunner) — it is never called
// from the request path.
//
// Safety against deleting an in-flight session:
//   - it takes the SAME per-session lock (sessionLockFor) that WriteChunk and
//     CompleteUpload take, so it never races with a chunk being written
//     nor with a session being finalized;
//   - the criterion is ONLY UpdatedAt — a freshly created session
//     (InitUpload), or one that received ANY chunk inside the window, is
//     never a candidate, no matter how many bytes it already holds;
//   - a session directory with no readable sidecar (session.json missing or
//     corrupted) uses the mtime of the DIRECTORY ITSELF as the equivalent
//     activity signal — without that, a corrupted session would never age
//     out and would leak disk forever, the very problem this reaper exists
//     to solve.
//
// It returns how many sessions were removed. Errors while processing ONE
// session (e.g. RemoveAll failed on permissions) are accumulated and returned
// at the end, but do not interrupt the sweep of the rest — one stuck session
// must not be able to block the cleanup of all the others.
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

// reapOneSessionIfStale decides and, where applicable, removes ONE session.
// Isolated by lock and by session so that ReapStaleUploadSessions can sweep
// sessions independently of one another.
func reapOneSessionIfStale(dataDir, sessionID string, cutoff time.Time) (removed bool, err error) {
	mu := sessionLockFor(sessionID)
	mu.Lock()
	defer mu.Unlock()

	lastActivity, ok := sessionLastActivity(dataDir, sessionID)
	if !ok {
		// Neither sidecar nor directory readable — nothing to do (the next sweep
		// tries again; it is not safe to assume "stale" on the strength of a
		// transient read error).
		return false, nil
	}
	if lastActivity.After(cutoff) {
		return false, nil // still inside the window — it may be in flight
	}
	if err := os.RemoveAll(stagingDir(dataDir, sessionID)); err != nil {
		return false, err
	}
	return true, nil
}

// sessionLastActivity returns the best signal of "the last time this session
// moved": the sidecar's UpdatedAt when it exists and is readable, or the
// staging directory's mtime when the sidecar is missing/corrupted.
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
