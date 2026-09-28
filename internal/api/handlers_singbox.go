package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/singbox"

	qrcode "github.com/skip2/go-qrcode"
)

const singboxClashSecret = "singbox_clash_secret"

func (r *Router) deviceRealityPort(name string) int {
	raw, err := os.ReadFile(r.cfg.SingboxDevicePortsPath)
	if err != nil {
		return 0
	}
	var m map[string]int
	if json.Unmarshal(raw, &m) != nil {
		return 0
	}
	return m[name]
}

func (r *Router) singboxManager() *singbox.Manager {
	reg := filepath.Join(r.cfg.DataDir, "singbox-devices.json")
	container := r.cfg.SingboxContainer
	restart := func(ctx context.Context) error {
		if r.docker == nil {
			return nil
		}
		return r.docker.Restart(ctx, container)
	}
	return singbox.New(r.cfg.SingboxConfigPath, reg, restart)
}

func (r *Router) singboxClash() *singbox.ClashClient {
	secret := ""
	if r.secrets != nil {
		if v, ok := r.secrets.Get(singboxClashSecret); ok {
			secret = strings.TrimSpace(v)
		}
	}
	return singbox.NewClash(r.cfg.SingboxClashURL, secret)
}

func (r *Router) handleTunnelUsage(w http.ResponseWriter, req *http.Request) {
	if auth.UserFrom(req) == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	if req.Method != http.MethodGet {
		writeErr(w, 405, "method not allowed")
		return
	}
	var usage any = []any{}
	if r.netUsage != nil {
		usage = r.netUsage.Snapshot()
	}
	writeJSON(w, map[string]any{"usage": usage})
}

func (r *Router) handleTunnelDevices(w http.ResponseWriter, req *http.Request) {
	if auth.UserFrom(req) == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	mgr := r.singboxManager()
	switch req.Method {
	case http.MethodGet:
		devs, err := mgr.List()
		if err != nil {
			writeErr(w, 503, "tunnel not configured ("+r.cfg.SingboxConfigPath+"): "+err.Error())
			return
		}
		summary, serr := r.singboxClash().Aggregate(req.Context())
		writeJSON(w, map[string]any{
			"devices": devs,
			"summary": summary,
			"live_ok": serr == nil,
		})
	case http.MethodPost:
		var body struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeErr(w, 400, "invalid body: "+err.Error())
			return
		}
		name := singbox.NormalizeName(body.Name)
		if !singbox.ValidName(name) {
			writeErr(w, 400, "invalid name: use lower-case letters, digits and hyphen (e.g. pc-sam)")
			return
		}
		d, err := mgr.Add(req.Context(), name)
		if err != nil {
			writeErr(w, 502, err.Error())
			return
		}
		link, _ := mgr.Link(d)
		writeJSON(w, map[string]any{"device": d, "link": link})
	default:
		writeErr(w, 405, "method not allowed")
	}
}

func (r *Router) handleTunnelDeviceAction(w http.ResponseWriter, req *http.Request) {
	if auth.UserFrom(req) == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	rest := strings.TrimPrefix(req.URL.Path, "/api/tunnel/devices/")
	parts := strings.SplitN(rest, "/", 2)
	uuid := parts[0]
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}
	if uuid == "" {
		writeErr(w, 400, "uuid missing")
		return
	}
	mgr := r.singboxManager()

	if req.Method == http.MethodDelete && action == "" {
		if err := mgr.Remove(req.Context(), uuid); err != nil {
			writeErr(w, 502, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true})
		return
	}

	switch action {
	case "exit":
		if req.Method != http.MethodPost {
			writeErr(w, 405, "method not allowed")
			return
		}
		var body struct {
			Exit string `json:"exit"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeErr(w, 400, "invalid body: "+err.Error())
			return
		}
		if err := mgr.SetExit(req.Context(), uuid, body.Exit); err != nil {
			writeErr(w, 502, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true, "exit": body.Exit})
	case "datasaver":
		if req.Method != http.MethodPost {
			writeErr(w, 405, "method not allowed")
			return
		}
		var body struct {
			On    bool `json:"on"`
			CaAck bool `json:"ca_ack"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeErr(w, 400, "invalid body: "+err.Error())
			return
		}
		if body.On && !body.CaAck {
			writeErr(w, 400, "data saving NOT turned on: routing this device's HTTPS through the compression proxy requires the data-saver CA installed ON IT — otherwise EVERY site stops loading. Resend with ca_ack=true confirming the CA is installed on this device.")
			return
		}
		if body.On {
			exit := singbox.ExitVPS
			if devs, lerr := mgr.List(); lerr == nil {
				for _, d := range devs {
					if d.UUID == uuid {
						exit = d.Exit
					}
				}
			}
			if _, perr := r.probeDatasaverProxy(req.Context(), exit); perr != nil {
				writeErr(w, 503, "data saving NOT turned on: the compression proxy of exit "+exit+" is not healthy ("+perr.Error()+"). Your device stays connected as usual (direct).")
				return
			}
		}
		if err := mgr.SetDatasaver(req.Context(), uuid, body.On); err != nil {
			writeErr(w, 502, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true, "datasaver": body.On})
	case "rename":
		if req.Method != http.MethodPost {
			writeErr(w, 405, "method not allowed")
			return
		}
		var body struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeErr(w, 400, "invalid body: "+err.Error())
			return
		}
		name := singbox.NormalizeName(body.Name)
		if err := mgr.Rename(req.Context(), uuid, name); err != nil {
			writeErr(w, 502, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true, "name": name})
	case "link", "qr":
		devs, err := mgr.List()
		if err != nil {
			writeErr(w, 503, err.Error())
			return
		}
		var found singbox.Device
		for _, d := range devs {
			if d.UUID == uuid {
				found = d
			}
		}
		if found.UUID == "" {
			writeErr(w, 404, "device not found")
			return
		}
		var link string
		variant := req.URL.Query().Get("variant")
		if variant == "reality" {
			link, err = mgr.LinkReality(found, r.deviceRealityPort(found.Name))
		} else {
			link, err = mgr.Link(found)
		}
		if err != nil {
			writeErr(w, 502, err.Error())
			return
		}
		if action == "link" {
			writeJSON(w, map[string]any{"link": link, "name": found.Name, "variant": variant})
			return
		}
		png, err := qrcode.Encode(link, qrcode.Medium, 256)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(png)
	default:
		writeErr(w, 404, "unknown action")
	}
}
