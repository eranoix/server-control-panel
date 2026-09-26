package api

// handlers_notify.go — CRUD + history for the event-driven notification spine
// All endpoints are primary-only (mustPrimary, mirroring the
// alerting handlers) and registered on the `protected` mux so auth.Middleware
// has populated the request context (feedback_gated_route_needs_middleware).
//
// Routes (sub-path actions + method switch, matching handlers_alerting.go's
// style rather than ServeMux {id} wildcards):
//
//   GET  /api/notify/rules            list rules
//   POST /api/notify/rules            upsert a rule (empty id → create)
//   POST /api/notify/rules/delete     {id}
//   GET  /api/notify/channels         list channels
//   POST /api/notify/channels         upsert a channel
//   POST /api/notify/channels/delete  {id}
//   POST /api/notify/channels/test    {id} — send a synthetic event now
//   POST /api/notify/dryrun           {rule} — preview matching recent events
//   GET  /api/notify/events           ?source=&type=&limit= history + dropped
//   GET  /api/notify/catalog          event-type + runner-kind catalog (UI)

import (
	"encoding/json"
	"net/http"
	"strconv"

	"server-control-panel/internal/notify"
)

// notifyReady gates every handler: 503 when the spine failed to boot.
func (r *Router) notifyReady(w http.ResponseWriter) bool {
	if r.notify == nil {
		writeErr(w, http.StatusServiceUnavailable, "notify unavailable")
		return false
	}
	return true
}

func (r *Router) handleNotifyRules(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.mustPrimary(w, req); !ok {
		return
	}
	if !r.notifyReady(w) {
		return
	}
	switch req.Method {
	case http.MethodGet:
		writeJSON(w, map[string]any{"rules": r.notify.Rules()})
	case http.MethodPost, http.MethodPut:
		var rl notify.Rule
		if err := json.NewDecoder(req.Body).Decode(&rl); err != nil {
			writeErr(w, 400, "bad json")
			return
		}
		saved, err := r.notify.UpsertRule(rl)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, saved)
	default:
		writeErr(w, 405, "method not allowed")
	}
}

func (r *Router) handleNotifyRuleDelete(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.mustPrimary(w, req); !ok {
		return
	}
	if !r.notifyReady(w) {
		return
	}
	if req.Method != http.MethodPost && req.Method != http.MethodDelete {
		writeErr(w, 405, "method not allowed")
		return
	}
	id := r.bodyOrQueryID(req)
	if id == "" {
		writeErr(w, 400, "id required")
		return
	}
	if err := r.notify.DeleteRule(id); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (r *Router) handleNotifyChannels(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.mustPrimary(w, req); !ok {
		return
	}
	if !r.notifyReady(w) {
		return
	}
	switch req.Method {
	case http.MethodGet:
		writeJSON(w, map[string]any{"channels": r.notify.ChannelDefsRedacted()})
	case http.MethodPost, http.MethodPut:
		var c notify.ChannelDef
		if err := json.NewDecoder(req.Body).Decode(&c); err != nil {
			writeErr(w, 400, "bad json")
			return
		}
		saved, err := r.notify.UpsertChannel(c)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, saved)
	default:
		writeErr(w, 405, "method not allowed")
	}
}

func (r *Router) handleNotifyChannelDelete(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.mustPrimary(w, req); !ok {
		return
	}
	if !r.notifyReady(w) {
		return
	}
	if req.Method != http.MethodPost && req.Method != http.MethodDelete {
		writeErr(w, 405, "method not allowed")
		return
	}
	id := r.bodyOrQueryID(req)
	if id == "" {
		writeErr(w, 400, "id required")
		return
	}
	if err := r.notify.DeleteChannel(id); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

// handleNotifyChannelTest sends a synthetic event through one channel right now,
// returning the real delivery outcome so the UI's "testar" button is honest.
func (r *Router) handleNotifyChannelTest(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.mustPrimary(w, req); !ok {
		return
	}
	if !r.notifyReady(w) {
		return
	}
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	id := r.bodyOrQueryID(req)
	if id == "" {
		writeErr(w, 400, "id required")
		return
	}
	ev := notify.Event{
		Type:     "test.notification",
		Severity: notify.SeverityInfo,
		Source:   "test",
		Title:    "Test notification ✅",
		Body:     "If you received this message, the channel is working.",
	}
	if err := r.notify.TestChannel(id, ev); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

// handleNotifyDryRun previews which recent history events a candidate rule would
// match — without sending anything. Builds confidence before saving.
func (r *Router) handleNotifyDryRun(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.mustPrimary(w, req); !ok {
		return
	}
	if !r.notifyReady(w) {
		return
	}
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	var rl notify.Rule
	if err := json.NewDecoder(req.Body).Decode(&rl); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	matches := r.notify.DryRun(rl)
	writeJSON(w, map[string]any{"matches": matches, "count": len(matches)})
}

// handleNotifyEvents returns the history feed (newest-first) plus the dropped
// counter — silence is never success, so the operator can see an overflowing bus.
func (r *Router) handleNotifyEvents(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.mustPrimary(w, req); !ok {
		return
	}
	if !r.notifyReady(w) {
		return
	}
	limit, _ := strconv.Atoi(req.URL.Query().Get("limit"))
	events := r.notify.History(notify.HistoryFilter{
		Source: req.URL.Query().Get("source"),
		Type:   req.URL.Query().Get("type"),
		Limit:  limit,
	})
	writeJSON(w, map[string]any{
		"events":  events,
		"dropped": r.notify.Dropped(),
	})
}

// handleNotifyInbox returns the in-app inbox (events routed to an "inapp"
// channel), newest-first — drives the topbar notification bell.
func (r *Router) handleNotifyInbox(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.mustPrimary(w, req); !ok {
		return
	}
	if !r.notifyReady(w) {
		return
	}
	limit, _ := strconv.Atoi(req.URL.Query().Get("limit"))
	writeJSON(w, map[string]any{"events": r.notify.Inbox(limit)})
}

// handleNotifyCatalog feeds the rule-builder + channel-builder UI: event types,
// the live runner-kind list, AND the channel-type catalog (which fields each
// channel needs), so the channel form can render type-specific inputs.
func (r *Router) handleNotifyCatalog(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.mustPrimary(w, req); !ok {
		return
	}
	kinds := make([]string, 0, len(r.queueRunners))
	for k := range r.queueRunners {
		kinds = append(kinds, k)
	}
	writeJSON(w, map[string]any{
		"event_types":   notifyEventCatalog,
		"severities":    []string{notify.SeverityInfo, notify.SeverityWarning, notify.SeverityCritical},
		"origins":       []string{"user", "scheduler", "ai"},
		"kinds":         kinds,
		"channel_types": notifyChannelCatalog,
	})
}

// notifyChannelCatalog describes each channel type for the UI form: an id, PT
// label, icon, one-line help, and the config fields to render. `fields` keys
// map to notify.ChannelConfig json tags. `secret:true` renders a password input
// (blank = keep on edit).
var notifyChannelCatalog = []map[string]any{
	{"type": notify.TypeInApp, "label": "In-app notification", "icon": "🔔",
		"help":   "Shows a notice on the 🔔 bell in the panel's bottom bar. Nothing external — works right away.",
		"fields": []map[string]any{}},
	{"type": "whatsapp", "label": "WhatsApp", "icon": "💬",
		"help": "Sends over WhatsApp through WAHA. Requires an account connected on the WhatsApp tab.",
		"fields": []map[string]any{
			{"key": "from_user", "label": "Sender account", "kind": "user"},
			{"key": "chat_jid", "label": "Destination chat (JID)", "kind": "text", "ph": "551199XXXXXXX ou 1234-5678@g.us", "mono": true},
		}},
	{"type": notify.TypeTelegram, "label": "Telegram", "icon": "✈️",
		"help": "Telegram bot. Create a bot with @BotFather and send it a message first.",
		"fields": []map[string]any{
			{"key": "bot_token", "label": "Bot token", "kind": "text", "secret": true, "ph": "123456:ABC-DEF...", "mono": true},
			{"key": "chat_id", "label": "Chat ID", "kind": "text", "ph": "e.g. 123456789 or -100123... (group)", "mono": true},
		}},
	{"type": notify.TypeEmail, "label": "Email (SMTP)", "icon": "✉️",
		"help": "Sends through an SMTP server. Port 587 (STARTTLS) or 465 (TLS).",
		"fields": []map[string]any{
			{"key": "smtp_host", "label": "SMTP server", "kind": "text", "ph": "smtp.gmail.com"},
			{"key": "smtp_port", "label": "Porta", "kind": "number", "ph": "587"},
			{"key": "smtp_user", "label": "Usuário", "kind": "text", "ph": "voce@gmail.com"},
			{"key": "smtp_pass", "label": "Password / app password", "kind": "text", "secret": true},
			{"key": "from", "label": "From", "kind": "text", "ph": "alertas@seudominio.com"},
			{"key": "to", "label": "To", "kind": "text", "ph": "you@email.com, someone@email.com"},
		}},
	{"type": notify.TypeWebhook, "label": "Webhook", "icon": "🔗",
		"help": "POST to a URL (Slack/Discord/n8n/ntfy/your own endpoint). By default it sends the " +
			"WHOLE event as JSON — title, body, labels. If the destination is public (a free-plan ntfy " +
			"topic, for instance), fill in the fixed body: then NOTHING of the event is sent.",
		"fields": []map[string]any{
			{"key": "url", "label": "URL", "kind": "text", "ph": "https://...", "mono": true},
			{"key": "fixed_body", "label": "Fixed body (public destination)", "kind": "text",
				"ph": "home: something needs attention. check the private channel.",
				"help": "Leave empty to send the event as JSON. When filled in, EXACTLY this text is what " +
					"goes out — the event never crosses, and the destination learns nothing about what broke."},
		}},
	{"type": notify.TypePush, "label": "Push (browser)", "icon": "📲",
		"help": "An operating-system notification, even with the panel closed — it uses the same " +
			"'Receive calls in the background' subscription from the video call settings. " +
			"Leave the user field empty to notify everyone subscribed.",
		"fields": []map[string]any{
			{"key": "to_user", "label": "This user only (optional)", "kind": "user"},
		}},
}

// notifyEventCatalog is the catalog the UI rule-builder renders. type_prefix
// is what a rule sets; "job." matches all four job.* terminal events.
var notifyEventCatalog = []map[string]string{
	{"type_prefix": "metric.threshold", "label": "Metric threshold crossed", "icon": "📊"},
	{"type_prefix": "metric.resolved", "label": "Metric recovered (back to normal)", "icon": "✅"},
	{"type_prefix": "job.", "label": "Task finished (any outcome)", "icon": "⚙"},
	{"type_prefix": "job.failed", "label": "Job failed", "icon": "❌"},
	{"type_prefix": "job.done", "label": "Task completed", "icon": "✅"},
	{"type_prefix": "job.cancelled", "label": "Task cancelled", "icon": "🚫"},
	{"type_prefix": "job.interrupted", "label": "Task interrupted (deploy/restart)", "icon": "⏸"},
	{"type_prefix": "scheduler.enqueue_failed", "label": "Schedule failed to start", "icon": "⏰"},
	{"type_prefix": "agent.waiting_input", "label": "Agent waiting for input", "icon": "⌨"},
	{"type_prefix": "agent.done", "label": "Agent finished the task", "icon": "🤖"},
}

// bodyOrQueryID extracts an id from either a JSON body {"id":...} or the ?id=
// query param, so delete/test work from forms and fetch alike.
func (r *Router) bodyOrQueryID(req *http.Request) string {
	if id := req.URL.Query().Get("id"); id != "" {
		return id
	}
	var body struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(req.Body).Decode(&body)
	return body.ID
}
