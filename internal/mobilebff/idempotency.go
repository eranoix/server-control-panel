package mobilebff

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Idempotencia stores the result of a mutation so that a RETRY of the same
// action does not execute it twice.
//
// # Why this exists
//
// The app's outbound queue (on Android) resends whatever it
// failed to deliver. Resending is only safe if the server can recognize that
// it has already seen that action — because **a timeout is indistinguishable
// from "it never arrived"**. Without that distinction, "rename the session"
// can happen twice, and the second rename either fails or renames the wrong
// session.
//
// That is why the app's queue used to accept ONE path only (sending a WhatsApp
// message, which proves idempotency through `client_msg_id` in the body). Its
// KDoc said, correctly, that opening the queue to everything else was "a
// SERVER change, not a client one". This is that server change.
//
// # What stays out
//
// Not every mutation should be re-executable later. Killing a session or
// firing a deploy ten minutes later, with nobody watching, is worse than
// failing right away: the world moved in the meantime. Those are deliberately
// left out — idempotency solves "it happened twice", it does not solve "it
// happened late".
//
// # Why it writes to disk
//
// A deploy restarts the process, and the gap between the action and the retry
// crosses restarts easily. A table living only in memory would lose exactly
// the keys that matter most — those of whoever was offline when the server
// came back up.
type Idempotency struct {
	mu      sync.Mutex
	file    string
	entries map[string]idempotentEntry
	now     func() time.Time
}

type idempotentEntry struct {
	// Body returned on the first run, repeated verbatim on the retry.
	Body string `json:"corpo"`
	// HTTP status of the first run.
	Status int   `json:"status"`
	At     int64 `json:"quando_ms"`
}

const (
	// A key is good for one day. Short enough that the table does not grow
	// without end, and long enough to cover a device that spent the night
	// offline — which is the case the queue exists to serve.
	idempotencyTTL = 24 * time.Hour

	// Cap on entries. Once exceeded, the oldest go. It is not a disk limit:
	// it is the same honesty limit the client queue has — ten thousand
	// pending actions mean something is wrong somewhere else.
	idempotencyCap = 10_000
)

// NewIdempotency loads (or creates) the table in dataDir.
func NewIdempotency(dataDir string) *Idempotency {
	i := &Idempotency{
		file:    filepath.Join(dataDir, "mobile-idempotencia.json"),
		entries: map[string]idempotentEntry{},
		now:     time.Now,
	}
	i.load()
	return i
}

// Recall returns the stored result for the key, if a valid one exists.
//
// The key MUST include the user: two accounts can generate the same client-side
// identifier, and a result leaking between them would be worse than having no
// idempotency at all.
func (i *Idempotency) Recall(key string) (body string, status int, ok bool) {
	if i == nil || key == "" {
		return "", 0, false
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	e, exists := i.entries[key]
	if !exists {
		return "", 0, false
	}
	if i.now().UnixMilli()-e.At > idempotencyTTL.Milliseconds() {
		delete(i.entries, key)
		return "", 0, false
	}
	return e.Body, e.Status, true
}

// Remember stores the result of this run.
func (i *Idempotency) Remember(key, body string, status int) {
	if i == nil || key == "" {
		return
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.entries[key] = idempotentEntry{
		Body:   body,
		Status: status,
		At:     i.now().UnixMilli(),
	}
	i.prune()
	i.save()
}

// prune removes what expired and, if still over the cap, the oldest.
// Called with the lock held.
func (i *Idempotency) prune() {
	limit := i.now().UnixMilli() - idempotencyTTL.Milliseconds()
	for k, e := range i.entries {
		if e.At < limit {
			delete(i.entries, k)
		}
	}
	for len(i.entries) > idempotencyCap {
		oldest, when := "", int64(1)<<62
		for k, e := range i.entries {
			if e.At < when {
				oldest, when = k, e.At
			}
		}
		if oldest == "" {
			return
		}
		delete(i.entries, oldest)
	}
}

func (i *Idempotency) load() {
	b, err := os.ReadFile(i.file)
	if err != nil {
		return
	}
	var loaded map[string]idempotentEntry
	if json.Unmarshal(b, &loaded) != nil {
		// Corrupt file: starting empty is the right degradation. The worst
		// that happens is a retry re-executing; wedging the BFF because a
		// cache table fails to deserialize would be out of all proportion.
		return
	}
	i.entries = loaded
}

// save persists. Called with the lock held.
func (i *Idempotency) save() {
	b, err := json.Marshal(i.entries)
	if err != nil {
		return
	}
	tmp := i.file + ".tmp"
	if os.WriteFile(tmp, b, 0o600) != nil {
		return
	}
	// Atomic rename: a process killed mid-write leaves the previous file
	// intact instead of a truncated JSON that the next load would discard
	// entirely.
	_ = os.Rename(tmp, i.file)
}

// persistFile is used by the tests to corrupt the file on purpose.
func persistFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}
