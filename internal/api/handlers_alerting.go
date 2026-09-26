package api

// handlers_alerting.go — regras de alerta + configuracao + teste WhatsApp
//
// Cobre:
//   - handleAlertList / Add / Remove / Fires (regras)
//   - handleAlertingConfig (GET/POST configuracoes de alerta)
//   - handleAlertingTest (envia mensagem de teste via canal configurado)
//   - normalizeAlertingJID (helper)
//
// Extraido de api.go.

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/config"
	"server-control-panel/internal/metrics"
)

// ---------- Alerts ----------

func (r *Router) handleAlertList(w http.ResponseWriter, req *http.Request) {
	st := r.alerts.Status()
	// Enrich with label/unit from the catalogue (resolved by the metric key).
	if r.metricReg != nil {
		for i := range st {
			if d, ok := r.metricReg.DescriptorFor(st[i].MetricKey); ok {
				st[i].Label = d.Label
				st[i].Unit = d.Unit
			}
		}
	}
	writeJSON(w, map[string]any{"rules": sanitizeList(st, "Name")})
}

// handleMetricsCatalog returns the metrics catalogue (descriptors) for the
// builder's metric selector and the mini-dashboard.
func (r *Router) handleMetricsCatalog(w http.ResponseWriter, req *http.Request) {
	if r.metricReg == nil {
		writeJSON(w, map[string]any{"metrics": []any{}})
		return
	}
	writeJSON(w, map[string]any{"metrics": r.metricReg.Catalog()})
}

// handleMetricsSnapshot returns the current values of every metric.
func (r *Router) handleMetricsSnapshot(w http.ResponseWriter, req *http.Request) {
	if r.metricReg == nil {
		writeJSON(w, map[string]any{"t": 0, "values": map[string]float64{}})
		return
	}
	writeJSON(w, r.metricReg.Latest())
}

// handleMetricsSeries returns the time series of ONE metric (?key=).
func (r *Router) handleMetricsSeries(w http.ResponseWriter, req *http.Request) {
	key := req.URL.Query().Get("key")
	if key == "" {
		writeErr(w, 400, "key required")
		return
	}
	if r.metricHist == nil {
		writeJSON(w, map[string]any{"key": key, "points": []any{}})
		return
	}
	writeJSON(w, map[string]any{"key": key, "points": r.metricHist.Series(key)})
}

// persistAlertRules best-effort saves rules to data/alert_rules.json so they
// survive restarts/deploys. Failures are logged, never block the request. The
// nil-guard makes it safe to call from recordFires on the edge transition, where
// a few tests construct a bare &Router{} with no cfg/alerts; the real call sites
// always have both.
func (r *Router) persistAlertRules() {
	if r.alerts == nil || r.cfg == nil {
		return
	}
	if err := r.alerts.Save(filepath.Join(r.cfg.DataDir, "alert_rules.json")); err != nil {
		log.Printf("alert rules save: %v", err)
	}
}

func (r *Router) handleAlertAdd(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	var rule metrics.Rule
	if err := json.NewDecoder(req.Body).Decode(&rule); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	// A canonical metric must exist in the catalogue (mirrors validSeverity/validField).
	if rule.Metric != "" && r.metricReg != nil {
		if _, ok := r.metricReg.DescriptorFor(rule.Metric); !ok {
			writeErr(w, 400, "unknown metric: "+rule.Metric)
			return
		}
	}
	if err := r.alerts.AddRule(rule); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	r.persistAlertRules()
	writeJSON(w, map[string]string{"status": "ok"})
}

func (r *Router) handleAlertRemove(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	r.alerts.RemoveRule(body.Name)
	r.persistAlertRules()
	writeJSON(w, map[string]string{"status": "ok"})
}

func (r *Router) handleAlertFires(w http.ResponseWriter, req *http.Request) {
	r.firesMu.Lock()
	defer r.firesMu.Unlock()
	cp := make([]metrics.Fire, len(r.fires))
	copy(cp, r.fires)
	writeJSON(w, cp)
}
func (r *Router) handleAlertingConfig(w http.ResponseWriter, req *http.Request) {
	user := auth.UserFrom(req)
	if !r.isPrimary(user) {
		writeErr(w, 403, "admin only")
		return
	}

	switch req.Method {
	case http.MethodGet:
		r.cfgMu.Lock()
		out := r.cfg.Alerting
		users := make([]string, 0, len(r.cfg.AllUsers()))
		for _, u := range r.cfg.AllUsers() {
			users = append(users, u.Username)
		}
		r.cfgMu.Unlock()
		writeJSON(w, map[string]any{
			"config":          out,
			"available_users": users,
		})
	case http.MethodPost:
		var body config.Alerting
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeErr(w, 400, "bad json")
			return
		}
		if body.Enabled {
			if body.FromUser == "" {
				writeErr(w, 400, "from_user required when enabled")
				return
			}
			if !r.cfg.HasUser(body.FromUser) {
				writeErr(w, 400, "from_user does not exist")
				return
			}
			if body.ChatJID == "" {
				writeErr(w, 400, "chat_jid required when enabled")
				return
			}
			// Normalize: a bare number becomes @c.us; @g.us/@c.us/@s.whatsapp.net are preserved.
			body.ChatJID = normalizeAlertingJID(body.ChatJID)
		}
		switch body.MinSeverity {
		case "", "info", "warning", "critical":
		default:
			writeErr(w, 400, "min_severity must be one of: '', info, warning, critical")
			return
		}

		r.cfgMu.Lock()
		r.cfg.Alerting = body
		err := config.Save(r.cfg, filepath.Join(r.cfg.DataDir, "config.json"))
		r.cfgMu.Unlock()
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		r.auditEvent(req, user, "alerting.update", "")
		writeJSON(w, map[string]any{"status": "ok"})
	default:
		writeErr(w, 405, "method not allowed")
	}
}

// handleAlertingTest fires a synthetic payload against our own
// /_internal/alert over loopback. It uses the same dispatch path as
// Alertmanager, so it validates the whole stack end-to-end (config + filter +
// resolve user + SendText). Returns the dispatch's HTTP status to the caller.
func (r *Router) handleAlertingTest(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	user := auth.UserFrom(req)
	if !r.isPrimary(user) {
		writeErr(w, 403, "admin only")
		return
	}

	now := time.Now().UTC()
	payload := map[string]any{
		"version":  "4",
		"groupKey": "test-from-vpsmanager",
		"status":   "firing",
		"receiver": "vps-manager-alert",
		"alerts": []map[string]any{
			{
				"status":      "firing",
				"labels":      map[string]string{"alertname": "TestAlert", "severity": "critical", "instance": "127.0.0.1:8765"},
				"annotations": map[string]string{"summary": "Test alert fired from the UI", "description": "Confirms the Prometheus→Alertmanager→vps-manager→WhatsApp stack."},
				"startsAt":    now.Format(time.RFC3339),
				"fingerprint": "test-vpsm-alert",
			},
		},
	}
	body, _ := json.Marshal(payload)
	listen := r.cfg.Listen
	if listen == "" {
		listen = "127.0.0.1:8765"
	}
	if strings.HasPrefix(listen, ":") {
		listen = "127.0.0.1" + listen
	}
	dispatchURL := "http://" + listen + "/_internal/alert"

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(dispatchURL, "application/json", bytes.NewReader(body))
	if err != nil {
		writeErr(w, 502, "dispatch failed: "+err.Error())
		return
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	r.auditEvent(req, user, "alerting.test", strconv.Itoa(resp.StatusCode))
	writeJSON(w, map[string]any{
		"status":   "ok",
		"upstream": resp.StatusCode,
		"body":     string(respBody),
	})
}

// normalizeAlertingJID appends "@c.us" when the user passed only the number with
// no suffix. Accepts the group format (already @g.us), individual (@c.us or
// @s.whatsapp.net), or a plain number. Trims whitespace and "+".
func normalizeAlertingJID(j string) string {
	j = strings.TrimSpace(j)
	j = strings.TrimPrefix(j, "+")
	if j == "" || strings.Contains(j, "@") {
		return j
	}
	return j + "@c.us"
}
