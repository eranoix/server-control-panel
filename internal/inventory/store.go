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

type Store struct {
	dataDir string
}

var inventoryFileMu sync.Mutex

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

func (s *Store) Path() string {
	return filepath.Join(s.dataDir, "inventory", "inventory.json")
}

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
		return Inventory{}, fmt.Errorf(
			"%s is at schema_version %d and this binary (%s) only understands up to %d: upgrade the binary before writing",
			s.Path(), inv.SchemaVersion, filepath.Base(os.Args[0]), SchemaVersion)
	}
	if inv.SchemaVersion == 0 {
		inv.SchemaVersion = SchemaVersion
	}
	return inv, nil
}

func (s *Store) Snapshot() (Inventory, error) {
	defer s.lock()()
	return s.load()
}

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

func (s *Store) persistLocked(inv Inventory) error {
	for _, n := range inv.Nodes {
		if err := n.Validate(); err != nil {
			return fmt.Errorf("refusing to write %s: %w", s.Path(), err)
		}
	}
	inv.SchemaVersion = SchemaVersion
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
	if df, err := os.Open(dir); err == nil {
		_ = df.Sync()
		_ = df.Close()
	}
	return nil
}
