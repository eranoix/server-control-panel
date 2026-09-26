package inventory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
)

// Store is the inventory persisted in `<dataDir>/inventory/inventory.json`.
//
// # Format: JSON, and by explicit decision
//
// Postgres in the `data` CT was REJECTED: the observer cannot depend on the
// observed — if `data` goes down, the panel would lose the inventory exactly when
// it is time to diagnose, and the symptom would be silence. SQLite too, for being
// a new dependency and departing from the shape of every other store in the repo
// (deploy, whatsapp, sessions, claudeacct). The inventory is small.
//
// # The two locks: copied from internal/deploy/store.go:80-100
//
// The comment over there documents a REAL bug (apps.json was overwritten
// whole under concurrent deploys) and the reason the mutex is PACKAGE-level: each
// Open() returns a different *Store — the queue runner and HTTP each have their own —
// so a per-instance mutex serializes nothing. The flock covers the
// CROSS-PROCESS case (the post-receive hook runs in vpsmctl, another process).
// The two together are what give atomic read-modify-write.
//
// # The write: copied from internal/config/migrate.go:451-482, NOT from deploy
//
// 🔴 `deploy.persistLocked` (store.go:149-165) is the INCOMPLETE analogue: it does
// os.WriteFile + os.Rename and syncs neither the file nor the directory. On an
// abrupt power cut the rename can reach the disk BEFORE the content, and
// a zero-byte file is left — which load() sees as "corrupted". This is not a
// hypothesis here: the UPS in this house exists but has NO data cable, which means
// a long outage ends in an abrupt cut.
// That is why persistence here uses the complete form, with Sync of the file and the
// directory (writeFileAtomic below).
type Store struct {
	dataDir string
}

// inventoryFileMu serialises inventory.json BETWEEN goroutines of the same
// process. Package-level for the same reason as appsFileMu in deploy/store.go:80.
var inventoryFileMu sync.Mutex

// lock takes the process mutex + an exclusive flock. Use: `defer s.lock()()`.
func (s *Store) lock() func() {
	inventoryFileMu.Lock()
	lp := filepath.Join(s.dataDir, "inventory", ".inventory.lock")
	_ = os.MkdirAll(filepath.Dir(lp), 0o700)
	f, err := os.OpenFile(lp, os.O_CREATE|os.O_RDWR, 0o600)
	if err == nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
	}
	return func() {
		if f != nil {
			_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
			_ = f.Close()
		}
		inventoryFileMu.Unlock()
	}
}

// Open prepares the store and VALIDATES what is already on disk. Constructor
// named `Open` because it does I/O, like deploy.Open and secrets.Open.
//
// A missing file is a LEGITIMATE empty inventory (first boot). An unreadable
// file is a HARD error: returning empty here would make the first write erase
// the whole inventory — the damage would be caused by the read, not by the
// failure.
func Open(dataDir string) (*Store, error) {
	s := &Store{dataDir: dataDir}
	if err := os.MkdirAll(filepath.Join(dataDir, "inventory"), 0o700); err != nil {
		return nil, fmt.Errorf("creating %s: %w", filepath.Join(dataDir, "inventory"), err)
	}
	defer s.lock()()
	if _, err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// Path is the path of the document. Exported because the caller's error message
// (and the tests) need to name the file, never to guess it.
func (s *Store) Path() string {
	return filepath.Join(s.dataDir, "inventory", "inventory.json")
}

// load reads the document. Call it under lock.
func (s *Store) load() (Inventory, error) {
	b, err := os.ReadFile(s.Path())
	if err != nil {
		if os.IsNotExist(err) {
			return Inventory{SchemaVersion: SchemaVersion}, nil
		}
		return Inventory{}, fmt.Errorf("reading %s: %w", s.Path(), err)
	}
	var inv Inventory
	if err := json.Unmarshal(b, &inv); err != nil {
		return Inventory{}, fmt.Errorf("%s corrupted: %w", s.Path(), err)
	}
	if inv.SchemaVersion > SchemaVersion {
		// A closed refusal, naming the binary — the same stance as
		// internal/config/config_io.go when it meets an unknown envelope.
		return Inventory{}, fmt.Errorf(
			"%s is at schema_version %d and this binary (%s) only understands up to %d: upgrade the binary before writing",
			s.Path(), inv.SchemaVersion, filepath.Base(os.Args[0]), SchemaVersion)
	}
	if inv.SchemaVersion == 0 {
		// A document with no version (written before this field existed) is read as
		// the current version and rewritten with it on the next write.
		inv.SchemaVersion = SchemaVersion
	}
	return inv, nil
}

// Snapshot returns the inventory as it stands on disk. It is always a COPY: the
// document is decoded from scratch on every call, so whoever receives it can
// poke at it freely without reaching anybody's state.
func (s *Store) Snapshot() (Inventory, error) {
	defer s.lock()()
	return s.load()
}

// Replace applies mutate to the inventory and persists it, ALL under a single
// lock — that is what avoids the lost update of the read-outside-the-lock →
// mutate → save pattern (the same care as deploy.UpdateEnv, store.go:196).
//
// If mutate produces an invalid node, nothing is written: the closed set of
// accepted values is only closed if the disk refuses it too.
func (s *Store) Replace(mutate func(*Inventory)) error {
	if mutate == nil {
		return fmt.Errorf("Replace without a mutation function")
	}
	defer s.lock()()
	inv, err := s.load()
	if err != nil {
		return err
	}
	mutate(&inv)
	return s.persistLocked(inv)
}

// persistLocked writes the document. Call it under lock.
func (s *Store) persistLocked(inv Inventory) error {
	for _, n := range inv.Nodes {
		if err := n.Validate(); err != nil {
			return fmt.Errorf("refusing to write %s: %w", s.Path(), err)
		}
	}
	inv.SchemaVersion = SchemaVersion
	// Stable order: a diff of a state file has to show a change of content, not
	// a reordering (the same reason as the sort in deploy/store.go:150).
	sort.Slice(inv.Nodes, func(i, j int) bool { return inv.Nodes[i].ID < inv.Nodes[j].ID })
	sort.Slice(inv.Services, func(i, j int) bool { return inv.Services[i].ID < inv.Services[j].ID })
	sort.Slice(inv.Projects, func(i, j int) bool { return inv.Projects[i].ID < inv.Projects[j].ID })
	sort.Slice(inv.Deployments, func(i, j int) bool { return inv.Deployments[i].ID < inv.Deployments[j].ID })
	sort.Slice(inv.Jobs, func(i, j int) bool { return inv.Jobs[i].ID < inv.Jobs[j].ID })

	b, err := json.MarshalIndent(inv, "", " ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.Path(), b, 0o600)
}

// writeFileAtomic is the atomic and DURABLE write, in the complete form of
// internal/config/migrate.go:451-482: temporary → Sync of the file → rename →
// Sync of the directory. The two Syncs are what is missing in
// deploy/store.go:149 and are the reason this file uses os.WriteFile nowhere.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp := path + ".new"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	// Sync of the FILE: guarantees the content reached the disk BEFORE the rename.
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	// Sync of the DIRECTORY: guarantees the rename itself survives the cut.
	if df, err := os.Open(dir); err == nil {
		_ = df.Sync()
		_ = df.Close()
	}
	return nil
}
