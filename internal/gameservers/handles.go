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

const defaultHandleTTL = 30 * time.Minute

type artifact struct {
	path      string
	server    string
	expires   time.Time
	ephemeral bool
}

type handleVault struct {
	mu    sync.Mutex
	items map[Handle]artifact
	ttl   time.Duration

	now func() time.Time
}

func newHandleVault(ttl time.Duration) *handleVault {
	if ttl <= 0 {
		ttl = defaultHandleTTL
	}
	return &handleVault{items: map[Handle]artifact{}, ttl: ttl, now: time.Now}
}

func (c *handleVault) Mint(serverID, path string, ephemeral bool) (Handle, error) {
	if serverID == "" || path == "" {
		return "", fmt.Errorf("handle requires server and path")
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
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

func (c *handleVault) Open(h Handle) (io.ReadCloser, error) {
	a, err := c.resolver(h, "")
	if err != nil {
		return nil, err
	}
	f, err := os.Open(a.path)
	if err != nil {
		return nil, fmt.Errorf("artifact unavailable: %w", err)
	}
	if !a.ephemeral {
		return f, nil
	}
	return &deleteOnClose{File: f, path: a.path, vault: c, h: h}, nil
}

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
