package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// cmdVideocall dispatches `vpsmctl videocall <sub>`. Operates on files
// directly (no HTTP to the panel) so it works even when vps-manager is
// down — the recovery story is "SSH in, run this command, get back online".
func cmdVideocall(args []string) error {
	if len(args) < 1 {
		videocallUsage()
		return nil
	}
	switch args[0] {
	case "init":
		return videocallInit(args[1:])
	case "status":
		return videocallStatus()
	case "start", "stop", "restart":
		return videocallSystemctl(args[0])
	case "logs":
		return videocallLogs(args[1:])
	case "-h", "--help", "help":
		videocallUsage()
		return nil
	default:
		return fmt.Errorf("unknown videocall subcommand: %s", args[0])
	}
}

func videocallUsage() {
	fmt.Fprint(os.Stderr, `vpsmctl videocall — TURN/STUN gateway control

Subcommands:
  init [--host=HOST] [--port=PORT]   provision coturn (idempotent)
  status                             container + reachability check
  start|stop|restart                 control vpsm-coturn.service
  logs [-n N]                        docker compose logs --tail N (default 100)

After init, restart vps-manager so the new TURN config is picked up:
  systemctl restart vps-manager
`)
}

const (
	vcEnvPath     = "/etc/vpsm/coturn.env"
	vcConfPath    = "/etc/coturn/turnserver.conf"
	vcTmplPath    = "/opt/panel/scripts/videocall/turnserver.conf.tmpl"
	vcComposePath = "/opt/panel/scripts/videocall/docker-compose.yml"
	vcDataDir     = "/var/lib/vpsm-coturn"
	vcServiceUnit = "vpsm-coturn.service"
)

func videocallInit(args []string) error {
	// Flags: --host=, --port= (optional overrides; otherwise auto-detect).
	flags := parseFlags(args)
	publicHost := flags["host"]
	if publicHost == "" {
		var err error
		publicHost, err = detectPublicIP()
		if err != nil {
			return fmt.Errorf("could not detect public IP — pass --host=YOUR_HOSTNAME_OR_IP: %w", err)
		}
		fmt.Printf("detected public host: %s (override with --host=)\n", publicHost)
	}
	port := flags["port"]
	if port == "" {
		port = "3478"
	}

	if err := os.MkdirAll("/etc/vpsm", 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll("/etc/coturn", 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(vcDataDir, 0o755); err != nil {
		return err
	}

	// Generate the HMAC secret only once. If /etc/vpsm/coturn.env already
	// exists with a TURN_SECRET, we reuse it — re-running `init` shouldn't
	// invalidate creds for an in-progress call.
	envVals, _ := readEnvFile(vcEnvPath)
	secret := envVals["TURN_SECRET"]
	if secret == "" {
		secret = randHex(32)
	}

	envOut := strings.Join([]string{
		"# coturn env — written by `vpsmctl videocall init`. Sourced by",
		"# /opt/panel/scripts/videocall/docker-compose.yml AND",
		"# /opt/panel (internal/api/api.go:loadTURNConfig). Keep both",
		"# sides in sync by re-running `vpsmctl videocall init` after edits.",
		"TURN_SECRET=" + secret,
		"TURN_PUBLIC_HOST=" + publicHost,
		"TURN_PORT=" + port,
		"TURN_MIN_PORT=49160",
		"TURN_MAX_PORT=49200",
		"",
	}, "\n")
	if err := writeFile(vcEnvPath, []byte(envOut), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", vcEnvPath, err)
	}
	fmt.Printf("wrote %s\n", vcEnvPath)

	// Render turnserver.conf from template.
	tmplBytes, err := os.ReadFile(vcTmplPath)
	if err != nil {
		return fmt.Errorf("template %s: %w", vcTmplPath, err)
	}
	conf := string(tmplBytes)
	repl := map[string]string{
		"{{TURN_PORT}}":        port,
		"{{TURN_MIN_PORT}}":    "49160",
		"{{TURN_MAX_PORT}}":    "49200",
		"{{TURN_REALM}}":       "vpsm.videocall",
		"{{TURN_SECRET}}":      secret,
		"{{EXTERNAL_IP_LINE}}": "external-ip=" + publicHost,
	}
	for k, v := range repl {
		conf = strings.ReplaceAll(conf, k, v)
	}
	if err := writeFile(vcConfPath, []byte(conf), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", vcConfPath, err)
	}
	fmt.Printf("wrote %s\n", vcConfPath)

	// Pull image up-front so the first `docker compose up` doesn't fail on a
	// slow network at the wrong moment.
	fmt.Println("pulling coturn image…")
	if err := dockerCompose("pull"); err != nil {
		fmt.Printf("WARN: docker compose pull: %v (will retry on up)\n", err)
	}

	// Install/refresh the systemd unit and bring the container up. We use a
	// thin systemd unit that wraps `docker compose up -d` so vpsm-coturn
	// behaves like every other vps-manager managed service.
	if err := writeSystemdUnit(); err != nil {
		return err
	}
	if out, err := exec.Command("systemctl", "daemon-reload").CombinedOutput(); err != nil {
		fmt.Print(string(out))
		return fmt.Errorf("daemon-reload: %w", err)
	}
	if out, err := exec.Command("systemctl", "enable", "--now", vcServiceUnit).CombinedOutput(); err != nil {
		fmt.Print(string(out))
		return fmt.Errorf("enable --now: %w", err)
	}
	fmt.Println("vpsm-coturn.service enabled and started")

	// Friendly post-install hints.
	fmt.Printf(`
=== Next steps ===

1) Open firewall (UFW example):
     sudo ufw allow %s/udp comment 'coturn TURN/STUN'
     sudo ufw allow 49160:49200/udp comment 'coturn relay range'

2) Restart vps-manager so it picks up /etc/vpsm/coturn.env:
     sudo systemctl restart vps-manager

3) Sanity check from another machine:
     turnutils_uclient -t -u test -w wrong %s
   (HMAC will reject, but you should see network reachability working)
`, port, publicHost)
	return nil
}

func videocallStatus() error {
	out, _ := exec.Command("systemctl", "is-active", vcServiceUnit).Output()
	fmt.Printf("systemd:          %s", out)
	dockerOut, _ := exec.Command("docker", "ps", "--filter", "name=vpsm-coturn", "--format",
		"table {{.Names}}\t{{.Status}}\t{{.Ports}}").Output()
	fmt.Print(string(dockerOut))
	envVals, err := readEnvFile(vcEnvPath)
	if err != nil {
		fmt.Println("env:              NOT INITIALIZED (run `vpsmctl videocall init`)")
		return nil
	}
	fmt.Printf("public host:      %s\n", envVals["TURN_PUBLIC_HOST"])
	fmt.Printf("listen port:      %s\n", envVals["TURN_PORT"])
	fmt.Printf("relay range:      %s-%s\n", envVals["TURN_MIN_PORT"], envVals["TURN_MAX_PORT"])
	fmt.Printf("secret length:    %d (HMAC SHA-1)\n", len(envVals["TURN_SECRET"]))
	return nil
}

func videocallSystemctl(action string) error {
	out, err := exec.Command("systemctl", action, vcServiceUnit).CombinedOutput()
	fmt.Print(string(out))
	if err != nil {
		return fmt.Errorf("systemctl %s %s: %w", action, vcServiceUnit, err)
	}
	fmt.Printf("systemctl %s %s — ok\n", action, vcServiceUnit)
	return nil
}

func videocallLogs(args []string) error {
	n := "100"
	if len(args) >= 2 && args[0] == "-n" {
		n = args[1]
	}
	cmd := exec.Command("docker", "compose", "-f", vcComposePath, "logs", "--tail", n, "--no-color")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// --- helpers --------------------------------------------------------------

func parseFlags(args []string) map[string]string {
	out := map[string]string{}
	for _, a := range args {
		if !strings.HasPrefix(a, "--") {
			continue
		}
		kv := strings.SplitN(strings.TrimPrefix(a, "--"), "=", 2)
		if len(kv) == 2 {
			out[kv[0]] = kv[1]
		}
	}
	return out
}

func readEnvFile(path string) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		out[strings.TrimSpace(line[:eq])] = strings.Trim(strings.TrimSpace(line[eq+1:]), `"'`)
	}
	return out, nil
}

func writeFile(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".new"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	_ = f.Close()
	return os.Rename(tmp, path)
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// detectPublicIP tries several strategies in order: the default-route IP (if
// it's globally routable), then a query to api.ipify.org. Returns the first
// non-private answer. Times out fast — the user can always override with
// --host=.
func detectPublicIP() (string, error) {
	if ip := defaultRouteIP(); ip != "" && !isPrivateIP(ip) {
		return ip, nil
	}
	ctx := &http.Client{Timeout: 3 * time.Second}
	resp, err := ctx.Get("https://api.ipify.org")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	ip := strings.TrimSpace(string(body))
	if net.ParseIP(ip) == nil {
		return "", fmt.Errorf("ipify returned non-IP %q", ip)
	}
	return ip, nil
}

func defaultRouteIP() string {
	// Trick: opening a UDP "connection" to a public IP forces the kernel to
	// resolve the default route, exposing our outgoing interface IP. No
	// packets are sent.
	conn, err := net.Dial("udp", "1.1.1.1:80")
	if err != nil {
		return ""
	}
	defer conn.Close()
	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return ""
	}
	return addr.IP.String()
}

func isPrivateIP(s string) bool {
	ip := net.ParseIP(s)
	if ip == nil {
		return true
	}
	return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast()
}

func writeSystemdUnit() error {
	const unitPath = "/etc/systemd/system/" + vcServiceUnit
	unit := `[Unit]
Description=vps-manager coturn (TURN/STUN for videocall)
After=docker.service network-online.target
Wants=docker.service network-online.target

[Service]
Type=oneshot
RemainAfterExit=true
ExecStart=/usr/bin/docker compose -f ` + vcComposePath + ` up -d
ExecStop=/usr/bin/docker compose -f ` + vcComposePath + ` down
TimeoutStartSec=120

[Install]
WantedBy=multi-user.target
`
	return writeFile(unitPath, []byte(unit), 0o644)
}

func dockerCompose(action string) error {
	cmd := exec.Command("docker", "compose", "-f", vcComposePath, action)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
