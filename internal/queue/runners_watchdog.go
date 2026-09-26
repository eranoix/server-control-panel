package queue

// runners_watchdog.go — disk + backup watchdog.
//
// One scheduled runner that folds two host-health signals into a single check:
//   (a) disk usage % of one (or two) paths against a threshold;
//   (b) age of the newest backup in a directory vs a max age, with a cheap
//       gzip integrity probe of that newest archive.
//
// Follows the established "fail on breach" contract: on any breach the runner
// returns an error, which the queue's terminal hook routes into the notify
// spine (WhatsApp/…) exactly like disk_check / ssl_check / rootkit_scan. When
// everything is healthy it logs an OK summary and returns nil. Primary-only,
// like every host-wide operational runner.

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
	DiskPath          string `json:"disk_path,omitempty"`            // default "/"
	DiskThreshold     int    `json:"disk_threshold,omitempty"`       // % (default 85)
	ExtraPath         string `json:"extra_path,omitempty"`           // optional second mount (e.g. /opt)
	BackupDir         string `json:"backup_dir,omitempty"`           // default <DataDir>/backups
	MaxBackupAgeHours int    `json:"max_backup_age_hours,omitempty"` // default 36
	SkipIntegrity     bool   `json:"skip_integrity,omitempty"`       // skip gzip -t on newest archive
}

type WatchdogRunner struct{ DataDir string }

func (WatchdogRunner) Kind() string                                { return "watchdog" }
func (WatchdogRunner) AuthorizedFor(_ string, isPrimary bool) bool { return isPrimary }

// diskUsedPct returns the used percentage plus total/avail bytes for path.
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

// newestFile returns the newest regular file directly under dir (non-recursive),
// its modtime, and whether one was found.
// newestFile: the newest file in dir, RECURSIVELY. The real backups live in
// subdirs (backups/state/, users/<u>/session-backups/), so a scan of the root
// level alone kept finding old junk at the top (a watchdog false positive).
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
	// Path hygiene: these come from job args (operator-authored, but validate
	// anyway — absolute, no traversal).
	for _, p := range []string{diskPath, a.ExtraPath, backupDir} {
		if p != "" && !absNoTraversal(p) {
			return errors.New("paths must be absolute and free of ..")
		}
	}

	var breaches []string

	// (a) disk usage — primary path, and optional second mount.
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

	// (b) backup freshness + cheap integrity probe.
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
		// Cheap integrity: gzip -t on gzip-family archives only. Best-effort —
		// a missing gzip binary is not a breach.
		if !a.SkipIntegrity && isGzipArchive(path) {
			if _, err := exec.LookPath("gzip"); err == nil {
				cmd := exec.CommandContext(ctx, "gzip", "-t", path)
				if out, err := cmd.CombinedOutput(); err != nil {
					fmt.Fprintf(logW, "integrity: FAILED (%s)\n", strings.TrimSpace(string(out)))
					breaches = append(breaches, "newest backup is corrupted: "+filepath.Base(path))
				} else {
					fmt.Fprintln(logW, "integridade: OK (gzip -t)")
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
