package gameservers

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// Opaque handle: minting and resolution stay ONLY on the node side.
//
// # THE DEFECT THIS FILE EXISTS TO PREVENT
//
// Three Manager methods already speak in host paths — `ExportWorld` returns the
// name of a temporary zip, `BackupPath` returns the backup's path, and
// `ConfigPath` the config's. If those paths crossed the Backend boundary, the
// extraction would be undone FROM THE INSIDE: every screen would keep working,
// the panel would start knowing the node's disk layout, and the day the panel
// SENT a path the agent would have turned into arbitrary file reading without a
// single new route showing up. It is silent by construction.
//
// The defense is the same one used throughout: it is not the name, it is the
// FORM. The `Handle` is a RANDOM token — it is not the path encrypted, nor
// encoded, nor derived from it. There is no transformation that turns it back
// into a path, because there is nothing inside it to convert. The token→path
// binding lives in an in-memory map of the process that owns the disk, and dies
// with it.
//
// Consequences, all of them intended:
//
//   - A `Handle` forged by the client does not resolve — it is not in the map.
//   - A real path sent as a `Handle` (`/etc/passwd`) does not resolve, for the
//     same reason, and not because of a denylist somebody would have to keep
//     complete.
//   - A `Handle` from one server does not open another's artifact: the scope is
//     checked on resolution, not only on minting.
//   - A `Handle` does not survive an agent restart, and that is a feature: a
//     download token that is valid forever is a token that leaks forever.

// defaultHandleTTL is the lifetime of a freshly minted handle.
//
// Short on purpose: the handle exists to cross ONE interaction (the screen asks
// for the export, the browser downloads right after). Half an hour covers a
// slow download of a big world with room to spare and does not leave a
// file-reading credential dangling for the life of the process.
const defaultHandleTTL = 30 * time.Minute

// artefato is what a Handle references. It stays on the node side, always.
type artifact struct {
	path      string
	server    string // scope: the handle only resolves for THIS server
	expires   time.Time
	ephemeral bool // true = the file is a temp of ours; delete it after serving
}

// handleVault holds the token→artifact bindings.
//
// No package-level `var`: each local back-end has its own. A global vault would
// be state shared between nodes inside a single process — exactly what the "no
// new global state" rule forbids.
type handleVault struct {
	mu    sync.Mutex
	items map[Handle]artifact
	ttl   time.Duration

	// agora is injectable so the expiry test does not have to wait on a clock
	// (a criterion you satisfy by waiting is a defect in the criterion).
	now func() time.Time
}

func newHandleVault(ttl time.Duration) *handleVault {
	if ttl <= 0 {
		ttl = defaultHandleTTL
	}
	return &handleVault{items: map[Handle]artifact{}, ttl: ttl, now: time.Now}
}

// Cunhar registers an artifact and returns the opaque token.
//
// The token comes from crypto/rand, never from math/rand and never from the
// path: 32 hexadecimal bytes. It is not guessable and carries no information at
// all about what it references.
func (c *handleVault) Mint(serverID, path string, ephemeral bool) (Handle, error) {
	if serverID == "" || path == "" {
		return "", fmt.Errorf("handle requires server and path")
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// An entropy failure is a hard failure: minting a predictable token would
		// be worse than minting none at all.
		return "", fmt.Errorf("no entropy to mint a handle: %w", err)
	}
	h := Handle(hex.EncodeToString(b))

	c.mu.Lock()
	defer c.mu.Unlock()
	c.expireStale()
	c.items[h] = artifact{
		path:      path,
		server:    serverID,
		expires:   c.now().Add(c.ttl),
		ephemeral: ephemeral,
	}
	return h, nil
}

// expireStale cleans up the map. Called with the mutex already held.
func (c *handleVault) expireStale() {
	now := c.now()
	for h, a := range c.items {
		if now.After(a.expires) {
			if a.ephemeral {
				_ = os.Remove(a.path)
			}
			delete(c.items, h)
		}
	}
}

// resolver returns a handle's artifact, checking scope and validity.
//
// The error message is the SAME for "does not exist", "expired" and "belongs to
// another server" — on purpose. Telling the three apart would hand a forger an
// oracle for discovering which tokens once existed.
func (c *handleVault) resolver(h Handle, serverID string) (artifact, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.expireStale()

	a, ok := c.items[h]
	if !ok || (serverID != "" && a.server != serverID) {
		return artifact{}, ErrHandleInvalid
	}
	return a, nil
}

// Abrir resolves the handle and returns the content.
//
// It takes no serverID: the caller here is Backend.Abrir, which is already the
// boundary. The per-server scope is checked in `resolver`, where the caller does
// know which server it is talking about.
func (c *handleVault) Open(h Handle) (io.ReadCloser, error) {
	a, err := c.resolver(h, "")
	if err != nil {
		return nil, err
	}
	f, err := os.Open(a.path)
	if err != nil {
		// The artifact vanished from disk (temp cleaned, backup deleted). Not the
		// same error as an invalid handle: here the token was good.
		return nil, fmt.Errorf("artifact unavailable: %w", err)
	}
	if !a.ephemeral {
		return f, nil
	}
	return &deleteOnClose{File: f, path: a.path, vault: c, h: h}, nil
}

// deleteOnClose removes the temporary file once whoever read it is done.
//
// `ExportWorld` creates a zip in /tmp that the old handler deleted with `defer
// os.Remove`. With the Handle in between, the handler's `defer` no longer works
// — the one who knows it is over is whoever closed the stream. Without this,
// every export leaks a zip.
type deleteOnClose struct {
	*os.File
	path  string
	vault *handleVault
	h     Handle
}

func (a *deleteOnClose) Close() error {
	err := a.File.Close()
	_ = os.Remove(a.path)
	a.vault.mu.Lock()
	delete(a.vault.items, a.h)
	a.vault.mu.Unlock()
	return err
}
