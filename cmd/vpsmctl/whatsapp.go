package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// cmdWhatsApp dispatches the `vpsmctl whatsapp <sub>` subcommands. Operates
// directly on the on-disk state file at data/whatsapp/state.json and on
// systemctl — does NOT call the vps-manager HTTP API. Designed to work when
// the panel itself is broken.
func cmdWhatsApp(args []string) error {
	if len(args) < 1 {
		whatsappUsage()
		return nil
	}
	switch args[0] {
	case "status":
		return whatsappStatus()
	case "qr":
		return whatsappQR()
	case "start":
		return whatsappSystemctl("start")
	case "stop":
		return whatsappSystemctl("stop")
	case "restart":
		return whatsappSystemctl("restart")
	case "logout":
		return whatsappLogout()
	case "logs":
		return whatsappLogs(args[1:])
	case "backup-session":
		return whatsappBackupSession(args[1:])
	case "wad-migrate":
		return whatsappWadMigrate(args[1:])
	case "wad-rollback":
		return whatsappWadRollback(args[1:])
	case "wad-fixmeta":
		return whatsappWadFixMeta(args[1:])
	case "-h", "--help", "help":
		whatsappUsage()
		return nil
	default:
		return fmt.Errorf("unknown whatsapp subcommand: %s", args[0])
	}
}

func whatsappUsage() {
	fmt.Fprint(os.Stderr, `vpsmctl whatsapp — WAHA gateway control

Subcommands:
  status                   show connection state from data/whatsapp/state.json
  qr                       save the current QR code as PNG to /tmp (if SCAN_QR_CODE)
  start                    systemctl start vpsm-whatsapp.service
  stop                     systemctl stop  vpsm-whatsapp.service
  restart                  systemctl restart vpsm-whatsapp.service
  logout                   docker exec WAHA to unpair this device
  logs [-n N]              docker compose logs --tail N (default 100)
  backup-session <dir>     tar /var/lib/vpsm-whatsapp/sessions to <dir>/wa-<ts>.tgz
  wad-migrate <user>       cut <user> over to the free whatsmeow daemon
  wad-rollback <user>      revert <user> back to WAHA
  wad-fixmeta <user>       re-sync the daemon's meta.json hmac_secret to the vault
                           (fixes silent live-push 401s; no re-pair) + reload session
`)
}

const (
	waStateFile = "/opt/panel/data/whatsapp/state.json"
	waCompose   = "/opt/vpsm-whatsapp/docker-compose.yml"
	waUnit      = "vpsm-whatsapp.service"
)

type waState struct {
	Status        string `json:"status"`
	Phone         string `json:"phone,omitempty"`
	PushName      string `json:"push_name,omitempty"`
	Engine        string `json:"engine,omitempty"`
	WAHAVersion   string `json:"waha_version,omitempty"`
	LastSyncTS    int64  `json:"last_sync_ts,omitempty"`
	LastQRTS      int64  `json:"last_qr_ts,omitempty"`
	QRDataURL     string `json:"qr_data_url,omitempty"`
	HookOK        bool   `json:"hook_ok"`
	HookLastErr   string `json:"hook_last_err,omitempty"`
	WAHAReachable bool   `json:"waha_reachable"`
	Enabled       bool   `json:"enabled"`
}

func readWAState() (*waState, error) {
	data, err := os.ReadFile(waStateFile)
	if err != nil {
		return nil, err
	}
	var st waState
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

func whatsappStatus() error {
	st, err := readWAState()
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("status: NOT INSTALLED (no state file at " + waStateFile + ")")
			return nil
		}
		return err
	}
	fmt.Println("=== vpsm-whatsapp ===")
	fmt.Printf("Status:           %s\n", st.Status)
	fmt.Printf("Engine:           %s\n", strOr(st.Engine, "unknown"))
	fmt.Printf("Phone:            %s\n", strOr(st.Phone, "—"))
	fmt.Printf("PushName:         %s\n", strOr(st.PushName, "—"))
	fmt.Printf("WAHA reachable:   %v\n", st.WAHAReachable)
	fmt.Printf("Webhook OK:       %v\n", st.HookOK)
	if st.HookLastErr != "" {
		fmt.Printf("Webhook lastErr:  %s\n", st.HookLastErr)
	}
	if st.LastSyncTS > 0 {
		fmt.Printf("Last sync:        %s\n", time.Unix(st.LastSyncTS, 0).Format(time.RFC3339))
	}
	if st.LastQRTS > 0 {
		fmt.Printf("Last QR:          %s\n", time.Unix(st.LastQRTS, 0).Format(time.RFC3339))
	}
	// systemd reality check
	out, _ := exec.Command("systemctl", "is-active", waUnit).Output()
	fmt.Printf("Systemd:          %s\n", strings.TrimSpace(string(out)))
	return nil
}

func whatsappQR() error {
	st, err := readWAState()
	if err != nil {
		return err
	}
	if st.QRDataURL == "" {
		fmt.Println("no QR available right now (status=" + st.Status + ")")
		return nil
	}
	// data:image/png;base64,XXX
	const prefix = "data:image/png;base64,"
	if !strings.HasPrefix(st.QRDataURL, prefix) {
		return fmt.Errorf("QR is not a PNG data URL")
	}
	raw, err := base64.StdEncoding.DecodeString(st.QRDataURL[len(prefix):])
	if err != nil {
		return err
	}
	dst := fmt.Sprintf("/tmp/vpsm-whatsapp-qr-%d.png", time.Now().Unix())
	if err := os.WriteFile(dst, raw, 0o600); err != nil {
		return err
	}
	fmt.Printf("QR saved to %s (%d bytes)\n", dst, len(raw))
	fmt.Println("Open on your machine: scp $(hostname):" + dst + " . && xdg-open *.png")
	return nil
}

func whatsappSystemctl(action string) error {
	out, err := exec.Command("systemctl", action, waUnit).CombinedOutput()
	fmt.Print(string(out))
	if err != nil {
		return fmt.Errorf("systemctl %s %s: %w", action, waUnit, err)
	}
	fmt.Printf("systemctl %s %s — ok\n", action, waUnit)
	return nil
}

func whatsappLogout() error {
	// Direct docker exec rather than HTTP so this works offline. We invoke
	// the WAHA API from inside the container using its own key (which lives
	// in /opt/vpsm-whatsapp/.env). Falls back to systemctl restart if exec
	// somehow fails — at worst, WAHA boots fresh and pairing remains.
	out, err := exec.Command("docker", "exec", "vpsm-whatsapp",
		"sh", "-c",
		`wget -q -O- --header="X-Api-Key: ${WHATSAPP_API_KEY}" `+
			`--post-data="" http://127.0.0.1:3000/api/sessions/default/logout`,
	).CombinedOutput()
	fmt.Print(string(out))
	if err != nil {
		return fmt.Errorf("docker exec logout: %w", err)
	}
	fmt.Println("\nlogout sent — pair again from the panel when ready")
	return nil
}

func whatsappLogs(args []string) error {
	n := "100"
	if len(args) >= 2 && args[0] == "-n" {
		n = args[1]
	}
	cmd := exec.Command("docker", "compose", "-f", waCompose, "logs", "--tail", n, "--no-color")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func whatsappBackupSession(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: vpsmctl whatsapp backup-session <dir>")
	}
	dir := args[0]
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("dest dir: %w", err)
	}
	ts := time.Now().UTC().Format("20060102-150405")
	dst := filepath.Join(dir, "vpsm-whatsapp-session-"+ts+".tgz")
	cmd := exec.Command("tar", "czf", dst, "-C", "/var/lib/vpsm-whatsapp", "sessions")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	fmt.Println("backup written to", dst)
	return nil
}

func strOr(a, b string) string {
	if a == "" {
		return b
	}
	return a
}
