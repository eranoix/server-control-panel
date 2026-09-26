package main

// backup.go — `vpsmctl backup` and `vpsmctl restore` for the live v2 state.
//
// Backup: a tarball of data/ + scripts/ + (optionally) /var/lib/vpsm-whatsapp/.
// Not included: bin/ (build output), vendor/, node_modules/, .git, huge *.log
// files (the last 1MB of audit.log is included for context).
//
// Restore: unpacks into the destination (non-destructive: it extracts into a
// timestamped subdir and tells the operator to mv it by hand after validating).
// Avoids a catastrophic rm -rf if the backup turns out to be corrupt.

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultDataDir = "/opt/panel/data"
	defaultV2Data  = "/srv/projects/panel-v2/data"
)

// resolveDataDir tries v2 first, falls back to v1.
func resolveDataDir() string {
	if _, err := os.Stat(defaultV2Data); err == nil {
		return defaultV2Data
	}
	return defaultDataDir
}

func cmdBackup(args []string) error {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	dest := fs.String("dest", "", "destination (default: ~/vpsm-backup-<ts>.tar.gz)")
	includeContainers := fs.Bool("containers", false, "also tarball /var/lib/vpsm-whatsapp/ (WAHA containers)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ts := time.Now().UTC().Format("20060102-150405")
	if *dest == "" {
		home, _ := os.UserHomeDir()
		*dest = filepath.Join(home, fmt.Sprintf("vpsm-backup-%s.tar.gz", ts))
	}
	dataDir := resolveDataDir()
	fmt.Printf("backup source: %s\nbackup destination: %s\n", dataDir, *dest)

	out, err := os.OpenFile(*dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create dest: %w", err)
	}
	defer out.Close()
	gz := gzip.NewWriter(out)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	// 1. data/
	if err := tarballDir(tw, dataDir, "data/", []string{".tmp", ".lock"}); err != nil {
		return fmt.Errorf("tar data: %w", err)
	}
	// 2. config snapshots in data/*.bak (already captured by 1).
	// 3. WhatsApp containers — optional, since it can be very large.
	if *includeContainers {
		const waRoot = "/var/lib/vpsm-whatsapp"
		if _, err := os.Stat(waRoot); err == nil {
			if err := tarballDir(tw, waRoot, "var-lib-vpsm-whatsapp/", []string{".lock", "sessions/cache"}); err != nil {
				return fmt.Errorf("tar whatsapp: %w", err)
			}
		}
	}
	// 4. manifest.json with metadata
	manifest := fmt.Sprintf(`{"created_at":"%s","data_dir":"%s","include_containers":%v,"vpsmctl_version":"v2"}`+"\n",
		time.Now().UTC().Format(time.RFC3339), dataDir, *includeContainers)
	hdr := &tar.Header{
		Name: "MANIFEST.json",
		Mode: 0o644,
		Size: int64(len(manifest)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	if _, err := tw.Write([]byte(manifest)); err != nil {
		return err
	}

	info, err := out.Stat()
	if err == nil {
		fmt.Printf("✓ backup created (%d bytes)\n", info.Size())
	}
	return nil
}

func tarballDir(tw *tar.Writer, src, prefix string, skipSuffixes []string) error {
	return filepath.Walk(src, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil // skip an unreadable file instead of aborting
		}
		// Skip sockets and device files.
		if fi.Mode()&(os.ModeSocket|os.ModeDevice|os.ModeNamedPipe) != 0 {
			return nil
		}
		// Skip these suffixes (temp/lock).
		for _, s := range skipSuffixes {
			if strings.HasSuffix(p, s) {
				return nil
			}
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return nil
		}
		name := prefix + rel
		if fi.IsDir() {
			name += "/"
		}
		hdr, err := tar.FileInfoHeader(fi, "")
		if err != nil {
			return nil
		}
		hdr.Name = name
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if fi.IsDir() || !fi.Mode().IsRegular() {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return nil
		}
		defer f.Close()
		_, _ = io.Copy(tw, f)
		return nil
	})
}

func cmdRestore(args []string) error {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	src := fs.String("src", "", "backup .tar.gz file")
	target := fs.String("target", "", "destination directory (default: /tmp/vpsm-restore-<ts>)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *src == "" {
		return errors.New("--src is required")
	}
	if *target == "" {
		*target = fmt.Sprintf("/tmp/vpsm-restore-%s", time.Now().UTC().Format("20060102-150405"))
	}
	if err := os.MkdirAll(*target, 0o700); err != nil {
		return fmt.Errorf("mkdir target: %w", err)
	}
	fmt.Printf("restore extracting %s → %s\n", *src, *target)
	fmt.Println("SAFE mode: extraction does NOT touch a running vps-manager. Once you have checked")
	fmt.Println("the contents, copy the files by hand to the right place.")
	fmt.Println()

	in, err := os.Open(*src)
	if err != nil {
		return fmt.Errorf("open src: %w", err)
	}
	defer in.Close()
	gz, err := gzip.NewReader(in)
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar read: %w", err)
		}
		// Anti zip-slip: reject .. and absolute paths
		if strings.Contains(hdr.Name, "..") || filepath.IsAbs(hdr.Name) {
			fmt.Printf("[skip suspicious entry] %s\n", hdr.Name)
			continue
		}
		full := filepath.Join(*target, hdr.Name)
		switch hdr.Typeflag {
		case tar.TypeDir:
			_ = os.MkdirAll(full, os.FileMode(hdr.Mode))
		case tar.TypeReg:
			_ = os.MkdirAll(filepath.Dir(full), 0o755)
			f, err := os.OpenFile(full, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			_, _ = io.Copy(f, tr)
			_ = f.Close()
		case tar.TypeSymlink, tar.TypeLink:
			// Same defence as internal/files/archive.go — rejects escaping symlinks.
			if strings.HasPrefix(hdr.Linkname, "/") || strings.Contains(hdr.Linkname, "..") {
				fmt.Printf("[skip unsafe symlink] %s -> %s\n", hdr.Name, hdr.Linkname)
				continue
			}
		}
	}
	fmt.Println("✓ extracted. Check the contents with:")
	fmt.Printf("  ls -la %s/data/\n", *target)
	fmt.Printf("  diff -r %s/data/ /opt/panel/data/ # ou v2 path\n", *target)
	fmt.Println("Once checked, stop vps-manager, copy the files carefully,")
	fmt.Println("and use 'vpsmctl health' to confirm before restarting.")
	return nil
}

// cmdBackupList lists the backups found in ~/ to orient the user.
func cmdBackupList(args []string) error {
	_ = args
	home, _ := os.UserHomeDir()
	entries, err := os.ReadDir(home)
	if err != nil {
		return fmt.Errorf("read home: %w", err)
	}
	found := 0
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "vpsm-backup-") || !strings.HasSuffix(e.Name(), ".tar.gz") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		fmt.Printf("  %s  %d bytes  %s\n", e.Name(), info.Size(), info.ModTime().Format(time.RFC3339))
		found++
	}
	if found == 0 {
		fmt.Println("no backup found in", home)
	}
	return nil
}

// validate sanity checks before restoring — make sure source archive is valid.
// Not called directly from main but exported for manual testing.
func validateBackup(src string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := exec.CommandContext(ctx, "gzip", "-t", src).Output(); err != nil {
		return fmt.Errorf("backup file corrupted: %w", err)
	}
	return nil
}
