package pty

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestListIncludesUnregisteredAliveSocket(t *testing.T) {
	dir := t.TempDir()
	sox := filepath.Join(dir, "session-sox")
	if err := os.MkdirAll(sox, 0o700); err != nil {
		t.Fatal(err)
	}
	sockPath := filepath.Join(sox, "Ghost.sock")
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		for {
			c, e := l.Accept()
			if e != nil {
				return
			}
			_ = c.Close()
		}
	}()

	reg, err := LoadRegistry(filepath.Join(dir, "reg.json"))
	if err != nil {
		t.Fatal(err)
	}
	b := newDtachBackend(dir, reg)
	list, err := b.List()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range list {
		if s["name"] == "Ghost" {
			found = true
		}
	}
	if !found {
		t.Errorf("List() did not include the live unregistered socket 'Ghost'; got: %+v", list)
	}
}
