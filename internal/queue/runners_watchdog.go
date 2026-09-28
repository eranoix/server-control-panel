package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type WatchdogArgs struct {
	DiskPath          string `json:"disk_path,omitempty"`
	DiskThreshold     int    `json:"disk_threshold,omitempty"`
	ExtraPath         string `json:"extra_path,omitempty"`
	BackupDir         string `json:"backup_dir,omitempty"`
	MaxBackupAgeHours int    `json:"max_backup_age_hours,omitempty"`
	SkipIntegrity     bool   `json:"skip_integrity,omitempty"`
}

type WatchdogRunner struct{ DataDir string }

func (WatchdogRunner) Kind() string                                { return "watchdog" }
func (WatchdogRunner) AuthorizedFor(_ string, isPrimary bool) bool { return isPrimary }

func diskUsedPct(path string) (pct int, total, avail uint64, err error) {
	var st syscall.Statfs_t
	if err = syscall.Statfs(path, &st); err != nil {
		return 0, 0, 0, err
	}
	bsize := uint64(st.Bsize)
	total = st.Blocks * bsize
	avail = st.Bavail * bsize
	if total == 0 {
		return 0, 0, 0, errors.New("total blocks = 0")
	}
	return int((total - avail) * 100 / total), total, avail, nil
}

func gibStr(b uint64) string { return fmt.Sprintf("%.1f GiB", float64(b)/(1<<30)) }

func newestFile(dir string) (path string, mod time.Time, found bool) {
	_ = filepath.Walk(dir, func(pth string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		if !found || info.ModTime().After(mod) {
			path, mod, found = pth, info.ModTime(), true
		}
		return nil
	})
	return path, mod, found
}

func (b WatchdogRunner) Run(ctx context.Context, args json.RawMessage, logW io.Writer, progress func(int), step func(string)) error {
	var a WatchdogArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return fmt.Errorf("args: %w", err)
		}
	}
	diskPath := a.DiskPath
	if diskPath == "" {
		diskPath = "/"
	}
	threshold := a.DiskThreshold
	if threshold <= 0 || threshold > 100 {
		threshold = 85
	}
	backupDir := a.BackupDir
	if backupDir == "" {
		backupDir = b.DataDir + "/backups"
	}
	maxAge := a.MaxBackupAgeHours
	if maxAge <= 0 {
		maxAge = 36
	}
	for _, p := range []string{diskPath, a.ExtraPath, backupDir} {
		if p != "" && !absNoTraversal(p) {
			return errors.New("paths must be absolute and free of ..")
		}
	}

	var breaches []string

	step("checking disk")
	checkDisk := func(path string) {
		pct, total, avail, err := diskUsedPct(path)
		if err != nil {
			fmt.Fprintf(logW, "%s: failed to measure (%v)\n", path, err)
			breaches = append(breaches, fmt.Sprintf("disk %s unreadable: %v", path, err))
			return
		}
		fmt.Fprintf(logW, "%s: used %s of %s (%d%%) — free %s\n", path, gibStr(total-avail), gibStr(total), pct, gibStr(avail))
		if pct >= threshold {
			breaches = append(breaches, fmt.Sprintf("disk %s at %d%% (limit %d%%)", path, pct, threshold))
		}
	}
	checkDisk(diskPath)
	if a.ExtraPath != "" && a.ExtraPath != diskPath {
		checkDisk(a.ExtraPath)
	}
	progress(50)

	step("checking backups")
	path, mod, found := newestFile(backupDir)
	if !found {
		fmt.Fprintf(logW, "%s: no backup found\n", backupDir)
		breaches = append(breaches, "no backup in "+backupDir)
	} else {
		age := time.Since(mod)
		fmt.Fprintf(logW, "newest backup: %s (%s ago)\n", filepath.Base(path), age.Round(time.Minute))
		if age > time.Duration(maxAge)*time.Hour {
			breaches = append(breaches, fmt.Sprintf("newest backup is %s old (limit %dh)", age.Round(time.Minute), maxAge))
		}
		if !a.SkipIntegrity && isGzipArchive(path) {
			if _, err := exec.LookPath("gzip"); err == nil {
				cmd := exec.CommandContext(ctx, "gzip", "-t", path)
				if out, err := cmd.CombinedOutput(); err != nil {
					fmt.Fprintf(logW, "integrity: FAILED (%s)\n", strings.TrimSpace(string(out)))
					breaches = append(breaches, "newest backup is corrupted: "+filepath.Base(path))
				} else {
					fmt.Fprintln(logW, "integrity: OK (gzip -t)")
				}
			}
		}
	}
	progress(100)

	if len(breaches) > 0 {
		return errors.New("watchdog: " + strings.Join(breaches, "; "))
	}
	fmt.Fprintln(logW, "✓ disk and backups OK")
	return nil
}

func isGzipArchive(p string) bool {
	l := strings.ToLower(p)
	return strings.HasSuffix(l, ".gz") || strings.HasSuffix(l, ".tgz")
}
