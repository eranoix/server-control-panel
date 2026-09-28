package files

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMobileList_Success(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "a-dir"), 0755); err != nil {
		t.Fatal(err)
	}

	res, err := MobileList(dir)
	if err != nil {
		t.Fatalf("MobileList: %v", err)
	}
	if len(res.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(res.Entries))
	}
	if !res.Entries[0].IsDir || res.Entries[0].Name != "a-dir" {
		t.Errorf("entries[0] = %+v, want a-dir (is_dir)", res.Entries[0])
	}
	if res.Entries[1].Name != "b.txt" || res.Entries[1].IsDir {
		t.Errorf("entries[1] = %+v, want b.txt (file)", res.Entries[1])
	}
}

func TestMobileList_RejectsFilePath(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(f, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := MobileList(f); err == nil {
		t.Fatal("MobileList on a file path should error")
	}
}

func TestMobileList_RejectsDenylistedPath(t *testing.T) {
	if _, err := MobileList("/etc/shadow"); err == nil {
		t.Fatal("MobileList on a denylisted path should error")
	}
}

func TestMobileRead_Success_LanguageHint(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "main.go")
	content := "package main\n"
	if err := os.WriteFile(f, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	res, err := MobileRead(f)
	if err != nil {
		t.Fatalf("MobileRead: %v", err)
	}
	if res.Content != content {
		t.Errorf("content = %q, want %q", res.Content, content)
	}
	if res.Language != "go" {
		t.Errorf("language = %q, want %q", res.Language, "go")
	}
	if res.Size != int64(len(content)) {
		t.Errorf("size = %d, want %d", res.Size, len(content))
	}
	if res.Mtime <= 0 {
		t.Errorf("mtime = %d, want > 0", res.Mtime)
	}
}

func TestMobileRead_BinaryRejected(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "bin.dat")
	if err := os.WriteFile(f, []byte{0x50, 0x00, 0x51}, 0644); err != nil {
		t.Fatal(err)
	}
	_, err := MobileRead(f)
	if !errors.Is(err, ErrBinary) {
		t.Fatalf("MobileRead on binary file: err = %v, want ErrBinary", err)
	}
}

func TestMobileRead_TooLargeRejected(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "big.txt")
	big := make([]byte, maxMobileReadSize+1)
	for i := range big {
		big[i] = 'a'
	}
	if err := os.WriteFile(f, big, 0644); err != nil {
		t.Fatal(err)
	}
	_, err := MobileRead(f)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("MobileRead on oversized file: err = %v, want ErrTooLarge", err)
	}
}

func TestMobileWrite_SuccessThenConflict(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "edit.txt")
	if err := os.WriteFile(f, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-1 * time.Hour).Truncate(time.Second)
	if err := os.Chtimes(f, stale, stale); err != nil {
		t.Fatal(err)
	}
	staleMtime := stale.Unix()

	newMtime, err := MobileWrite(f, "first edit", staleMtime)
	if err != nil {
		t.Fatalf("first MobileWrite: %v", err)
	}
	if newMtime <= 0 {
		t.Errorf("newMtime = %d, want > 0", newMtime)
	}
	if newMtime == staleMtime {
		t.Fatalf("newMtime (%d) equals the forced staleMtime — the test would prove nothing", newMtime)
	}
	got, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "first edit" {
		t.Fatalf("content after first write = %q, want %q", got, "first edit")
	}

	if _, err := MobileWrite(f, "second edit (should not land)", staleMtime); !errors.Is(err, ErrConflict) {
		t.Fatalf("second MobileWrite: err = %v, want ErrConflict", err)
	}
	got, err = os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "first edit" {
		t.Fatalf("content after conflicting write = %q, want unchanged %q", got, "first edit")
	}
}

func TestMobileWrite_ZeroMtimeCreatesUnconditionally(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "nested", "new.txt")
	if _, err := MobileWrite(f, "brand new", 0); err != nil {
		t.Fatalf("MobileWrite (new file): %v", err)
	}
	got, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "brand new" {
		t.Fatalf("content = %q, want %q", got, "brand new")
	}
}

func TestMobileWrite_ConflictWhenFileDeleted(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "gone.txt")
	if err := os.WriteFile(f, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	read, err := MobileRead(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(f); err != nil {
		t.Fatal(err)
	}
	if _, err := MobileWrite(f, "y", read.Mtime); !errors.Is(err, ErrConflict) {
		t.Fatalf("MobileWrite on deleted file with nonzero expectedMtime: err = %v, want ErrConflict", err)
	}
}

func TestMobileRead_SymlinkEscapesDenylist_Rejected(t *testing.T) {
	decoy := "/opt/panel/data/secrets-mobile-adapter-test-decoy"
	if err := os.WriteFile(decoy, []byte("secret-must-not-leak"), 0600); err != nil {
		t.Skipf("could not create the decoy at %s (environment cannot write to that path): %v", decoy, err)
	}
	t.Cleanup(func() { _ = os.Remove(decoy) })

	dir := t.TempDir()
	link := filepath.Join(dir, "innocuous-looking-file")
	if err := os.Symlink(decoy, link); err != nil {
		t.Fatalf("os.Symlink: %v", err)
	}

	if _, err := MobileRead(link); err == nil {
		t.Fatal("MobileRead followed a symlink into the denylist without an error — path traversal via symlink was not blocked")
	}
	if _, err := MobileList(dir); err != nil {
		t.Fatalf("MobileList on the directory containing the symlink should not fail (the symlink itself is just a listed entry, not followed): %v", err)
	}
	if _, err := MobileWrite(link, "overwrite attempt", 0); err == nil {
		t.Fatal("MobileWrite followed a symlink into the denylist without an error — overwrite via path traversal was not blocked")
	}

	got, err := os.ReadFile(decoy)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "secret-must-not-leak" {
		t.Fatal("the decoy was overwritten — MobileWrite should not have touched it")
	}
}

func TestMobileWrite_SymlinkParentEscapesDenylist_Rejected(t *testing.T) {
	decoyDir := "/opt/panel/data/secrets-mobile-adapter-test-decoy-dir"
	if err := os.MkdirAll(decoyDir, 0700); err != nil {
		t.Skipf("could not create the decoy directory at %s: %v", decoyDir, err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(decoyDir) })

	dir := t.TempDir()
	linkDir := filepath.Join(dir, "looks-like-a-normal-dir")
	if err := os.Symlink(decoyDir, linkDir); err != nil {
		t.Fatalf("os.Symlink: %v", err)
	}

	newFile := filepath.Join(linkDir, "new-file.txt")
	if _, err := MobileWrite(newFile, "must not be created inside the decoy", 0); err == nil {
		t.Fatal("MobileWrite created a file through a directory symlink into the denylist — path traversal was not blocked")
	}
	if _, err := os.Stat(filepath.Join(decoyDir, "new-file.txt")); err == nil {
		t.Fatal("the file was created inside the decoy directory — the write should have been blocked before touching the disk")
	}
}

func TestMobileWrite_ConcurrentSamePath_Serialized(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "concurrent.txt")
	if err := os.WriteFile(f, []byte("start"), 0644); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 2)
	go func() {
		_, err := MobileWrite(f, "from goroutine A", 0)
		done <- err
	}()
	go func() {
		_, err := MobileWrite(f, "from goroutine B", 0)
		done <- err
	}()

	timeout := time.After(5 * time.Second)
	for i := 0; i < 2; i++ {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("concurrent MobileWrite: %v", err)
			}
		case <-timeout:
			t.Fatal("timed out waiting for the concurrent writes")
		}
	}

	got, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "from goroutine A" && string(got) != "from goroutine B" {
		t.Fatalf("final content corrupted/interleaved: %q", got)
	}
}
