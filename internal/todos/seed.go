package todos

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// SuggestSeed reads the real host state and returns TODO candidates the
// operator might want. Empty result is fine and means "nothing interesting
// to seed right now". This function is intentionally read-only — the caller
// chooses which suggestions to actually persist.
//
// Sources, in order:
//   - TLS cert at <DataDir>/tls.crt expiring in <60d → ssl_renewal
//   - apt last-update older than 14d → apt_upgrade (recurring 14d)
//   - secrets.vault mtime older than 90d → secret_rotation (recurring 90d)
//   - uptime > 30d → reboot_due
func SuggestSeed(ctx context.Context, dataDir string) []Todo {
	var out []Todo
	now := time.Now()

	// TLS expiry
	if t := tlsExpiry(filepath.Join(dataDir, "tls.crt")); !t.IsZero() {
		days := int(time.Until(t).Hours() / 24)
		if days < 60 {
			out = append(out, Todo{
				Title:    "Renew TLS certificate (" + t.Format("2006-01-02") + ")",
				Notes:    "Certbot/CertMagic failed or the cert expires in " + itoa(days) + " days. Check the auto-renew logs.",
				Category: CatSSLRenewal,
				Due:      t.Add(-7 * 24 * time.Hour).Unix(),
			})
		}
	}

	// apt last update
	if age := aptLastUpdateAge(); age > 14*24*time.Hour {
		out = append(out, Todo{
			Title:        "Run apt update + upgrade",
			Notes:        "Last update was " + age.Truncate(time.Hour).String(),
			Category:     CatAptUpgrade,
			IntervalDays: 14,
			Due:          now.Add(24 * time.Hour).Unix(),
		})
	}

	// secrets rotation
	if info, err := os.Stat(filepath.Join(dataDir, "secrets.vault")); err == nil {
		age := now.Sub(info.ModTime())
		if age > 90*24*time.Hour {
			out = append(out, Todo{
				Title:        "Rotate vault secrets (HMAC_KEY, etc.)",
				Notes:        "secrets.vault has not changed for " + age.Truncate(24*time.Hour).String(),
				Category:     CatSecretRotation,
				IntervalDays: 90,
				Due:          now.Add(7 * 24 * time.Hour).Unix(),
			})
		}
	}

	// uptime
	if up := uptime(); up > 30*24*time.Hour {
		out = append(out, Todo{
			Title:    "Consider a scheduled reboot",
			Notes:    "Current uptime: " + up.Truncate(time.Hour).String() + ". The kernel may have pending updates.",
			Category: CatRebootDue,
			Due:      now.Add(7 * 24 * time.Hour).Unix(),
		})
	}

	return out
}

func tlsExpiry(path string) time.Time {
	raw, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return time.Time{}
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return time.Time{}
	}
	_ = tls.Config{} // import touch; keeps imports honest if pem path ever fails
	return cert.NotAfter
}

func aptLastUpdateAge() time.Duration {
	for _, p := range []string{
		"/var/lib/apt/periodic/update-success-stamp",
		"/var/cache/apt/pkgcache.bin",
	} {
		if info, err := os.Stat(p); err == nil {
			return time.Since(info.ModTime())
		}
	}
	return 0
}

func uptime() time.Duration {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0
	}
	sec := parseFloat(fields[0])
	return time.Duration(sec) * time.Second
}

func parseFloat(s string) float64 {
	// minimal float parser to avoid strconv.ParseFloat import here
	var n float64
	var dec, div float64 = 0, 10
	seenDot := false
	for _, c := range s {
		if c == '.' {
			seenDot = true
			continue
		}
		if c < '0' || c > '9' {
			break
		}
		if seenDot {
			dec += float64(c-'0') / div
			div *= 10
		} else {
			n = n*10 + float64(c-'0')
		}
	}
	return n + dec
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [10]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

// keep go vet happy about exec import (used by future hooks)
var _ = exec.Command
