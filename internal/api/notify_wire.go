package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"

	"server-control-panel/internal/httpx"
	"server-control-panel/internal/metrics"
	"server-control-panel/internal/mobilebff"
	"server-control-panel/internal/notify"
	"server-control-panel/internal/notify/fcmpush"
	"server-control-panel/internal/notify/wachannel"
	"server-control-panel/internal/queue"
	"server-control-panel/internal/whatsapp"
)

// notify_wire.go is the seam between the notification spine (internal/notify,
// which knows nothing of jobs/queue/config) and the app. It builds the Router,
// maps a finished queue.Job into a notify.Event, wires the queue terminal hook,
// and seeds a first-run rule so the core case (alerting on job errors and
// completions) works out of the box.

// fcmServiceAccountSecret is the vault key (internal/secrets) holding the FCM
// service-account JSON credential. Provisioned by a human via
// `vpsmctl secrets set --user sam fcm_service_account` — never generated,
// printed, or hardcoded here. Absent secret ⇒ initFCMSender logs and returns
// nil, and native Android push degrades to zero deliveries (webpush keeps
// working) instead of panicking, matching every other channel's degrade-on-
// missing-config behavior in this file.
const fcmServiceAccountSecret = "fcm_service_account"

// initFCMSender loads the FCM service-account credential from the vault and
// builds a *fcmpush.Sender backed by deviceStore. Returns nil (not an error)
// when the secret is absent or r.secrets itself is nil — this is an expected,
// diagnosable degrade, not a boot failure.
func (r *Router) initFCMSender(deviceStore fcmpush.DeviceStore) *fcmpush.Sender {
	if r.secrets == nil {
		return nil
	}
	saJSON, ok := r.secrets.Get(fcmServiceAccountSecret)
	if !ok || strings.TrimSpace(saJSON) == "" {
		log.Printf("notify: native FCM push disabled (set the %q secret to the service account JSON)", fcmServiceAccountSecret)
		return nil
	}
	sender, err := fcmpush.NewSender(context.Background(), []byte(saJSON), deviceStore)
	if err != nil {
		log.Printf("notify: native FCM push disabled (invalid credential: %v)", err)
		return nil
	}
	return sender
}

// initNotify constructs the Router, registers channels, seeds defaults, and
// wires the queue terminal-job hook. Non-fatal: on failure the app still boots
// and the hook stays nil (fail closed — nothing notifies, nothing breaks).
func (r *Router) initNotify() {
	// Registered Android devices — the SAME pair of stores that
	// push_devices.go/notify_prefs.go use through the mobile BFF's Deps
	// (see mobileDeps further down, in api.go), built here exactly once
	// because both the FCM pushSender and the "push" channel's
	// DevicePrefsResolver need it.
	r.pushDevices = mobilebff.NewDeviceTokenStore(r.cfg.DataDir)
	r.pushDevicePrefs = mobilebff.NewDevicePrefsStore(r.cfg.DataDir)

	// r.fcmSender is kept on the Router (not merely in a local variable) so that
	// videocall.Open (further down in NewRouter) reuses this SAME instance in
	// Options.FCM — never a second fcmpush.NewSender(...), which would require a
	// second read of the credential and create two HTTP clients for the same
	// destination.
	r.fcmSender = r.initFCMSender(r.pushDevices)

	rt, err := notify.New(notify.Options{
		DataDir: r.cfg.DataDir,
		Channels: map[string]notify.Channel{
			// Lazy provider: r.whatsappMgr is assigned a few lines later in
			// NewRouter, after this runs. Resolving it at send time avoids any
			// boot-ordering fragility.
			wachannel.Type:      wachannel.New(func() *whatsapp.Manager { return r.whatsappMgr }),
			notify.TypeTelegram: notify.NewTelegramChannel(),
			notify.TypeEmail:    notify.NewEmailChannel(),
			notify.TypeWebhook:  notify.NewWebhookChannel(),
			// r.webpush may be nil (VAPID keygen failed at boot); r.fcmSender
			// may be nil (no FCM credential yet) — NewWebpushSender/
			// NewFCMSender both tolerate that and degrade to "delivers
			// nothing" instead of panicking, same as every other channel
			// degrades on missing config. r.pushDevicePrefs implements
			// notify.DevicePrefsResolver structurally (the per-device/
			// per-Rule filter).
			notify.TypePush: notify.NewPushChannel(
				notify.FanOutSenders(notify.NewWebpushSender(r.webpush), notify.NewFCMSender(r.fcmSender)),
				r.pushDevicePrefs,
			),
		},
	})
	if err != nil {
		log.Printf("notify disabled: %v", err)
		return
	}
	r.notify = rt
	// In-app channel needs the Router (its sink is InboxAdd) — wire after New.
	// Also bridges every in-app event onto /ws/mobile-events' "notify.inbox"
	// channel so a connected app sees it live, not only via the
	// existing GET /api/notify/inbox poll. r.mobileHub is assigned later in
	// NewRouter (mux setup); bridgeNotifyInboxToHub tolerates it still being
	// nil at any given Send (no subscriber can exist before the Hub does).
	rt.AddChannelImpl(notify.TypeInApp, notify.NewInAppChannel(func(ev notify.Event) {
		rt.InboxAdd(ev)
		r.bridgeNotifyInboxToHub(ev)
	}))
	r.seedNotifyDefaults()

	if r.queue != nil {
		// SetNotifier installs the hook the 5 terminal sites call. Dispatch is
		// non-blocking and never touches queue.mu, so this closure is safe to
		// invoke directly under the lock.
		r.queue.SetNotifier(func(j *queue.Job) {
			r.notify.Dispatch(jobEvent(j))
			// Chaining + notify-on-completion through the chosen channel.
			if r.scheduler != nil {
				r.scheduler.OnQueueTerminal(j.Source, string(j.Status))
				if id := schedSourceID(j.Source); id != "" {
					if sj, err := r.scheduler.Get(id); err == nil && len(sj.NotifyChannels) > 0 {
						ok := j.Status == queue.StatusDone
						on := sj.NotifyOn
						send := on == "always" || (on == "failure" && !ok) || ((on == "" || on == "success") && ok)
						if send {
							r.notify.SendToChannels(sj.NotifyChannels, jobEvent(j))
						}
					}
				}
			}
		})
	}
}

// bridgeNotifyInboxToHub forwards an in-app notify Event onto
// /ws/mobile-events' "notify.inbox" channel, so a connected app
// sees a new notification live instead of only via the existing GET
// /api/notify/inbox poll. No-op if r.mobileHub isn't wired yet (it is
// assigned later in NewRouter's mux setup — before the mux ever serves a
// request, so no live connection can miss this) or the event fails to
// marshal. Visible to the event's Owner (or every connected user, for
// system-wide events with no Owner) or an admin — the same shape
// bridgeSelfDeployJob/StartOpsHealthPublisher already use for their own Hub
// publishes.
func (r *Router) bridgeNotifyInboxToHub(ev notify.Event) {
	if r.mobileHub == nil {
		return
	}
	data, err := json.Marshal(ev)
	if err != nil {
		return
	}
	owner := ev.Owner
	r.mobileHub.Publish("notify.inbox", mobilebff.Envelope{Type: ev.Type, Data: data}, func(user string) bool {
		return owner == "" || user == owner || httpx.IsAdmin(r.cfg, user)
	})
}

// jobEvent maps a finished job to a notification Event. The Labels expose the
// discriminators rules route by: kind (which runner), origin (user/scheduler/
// ai, normalized from Source), and the raw job_id/source.
func jobEvent(j *queue.Job) notify.Event {
	sev := notify.SeverityInfo
	switch j.Status {
	case queue.StatusFailed, queue.StatusInterrupted:
		sev = notify.SeverityCritical
	case queue.StatusDone, queue.StatusCancelled:
		sev = notify.SeverityInfo
	}
	return notify.Event{
		Type:     "job." + string(j.Status),
		Severity: sev,
		Source:   j.Source,
		Owner:    j.Owner,
		Title:    jobTitle(j),
		Body:     j.Error,
		Labels: map[string]string{
			"kind":   j.Kind,
			"source": j.Source,
			"job_id": j.ID,
			"origin": originOf(j.Source),
		},
		TS:       j.Finished,
		DedupKey: "job:" + j.ID,
	}
}

// mapSeverity converts a rule's severity string into a notify severity.
// Empty/unknown defaults to Warning, preserving the legacy behavior for rules
// created before per-rule severity existed.
func mapSeverity(s string) string {
	switch s {
	case "info":
		return notify.SeverityInfo
	case "critical":
		return notify.SeverityCritical
	default:
		return notify.SeverityWarning
	}
}

// metricEvent maps a metrics Fire to a notification Event. Fires are now edge
// transitions in the Engine (one crossing, one recovery per episode),
// so the "notify once" correctness lives at the source — the Router throttle is
// just fan-out + a backstop. The DedupKey is keyed per EPISODE (the ActiveSince
// that opened it), not per rule, so a fresh episode after a recovery is never
// swallowed by the previous episode's throttle window.
//
// A Resolved fire becomes a distinct metric.resolved info event so an operator
// can opt into "tell me when it recovers" via a rule, while threshold rules that
// only want the crossing are unaffected (exact "metric.threshold" rules do not
// match "metric.resolved"; broad "metric." prefix rules receive both).
func metricEvent(f metrics.Fire) notify.Event {
	ep := strconv.FormatInt(f.Episode, 10)
	if f.Resolved {
		return notify.Event{
			Type:     notify.TypeMetricResolved,
			Severity: notify.SeverityInfo,
			Source:   "metrics",
			Title:    "Metric recovered: " + f.Rule,
			Body:     fmt.Sprintf("value %.2f is back to normal", f.Value),
			Labels:   map[string]string{"rule": f.Rule},
			TS:       f.Time,
			DedupKey: "metric-res:" + f.Rule + ":" + ep,
		}
	}
	// A RenotifySec reminder reuses the open episode but fires at a later Time;
	// keying it by Time keeps each reminder a distinct event so the Router's
	// throttle window (which would otherwise cap one-per-300s per episode) lets
	// the operator-requested cadence through. The initial crossing has
	// Time == Episode, so its key stays the stable per-episode "metric:<rule>:<ep>".
	key := "metric:" + f.Rule + ":" + ep
	if f.Episode != 0 && f.Time != f.Episode {
		key += ":r" + strconv.FormatInt(f.Time, 10)
	}
	return notify.Event{
		Type:     notify.TypeMetricThreshold,
		Severity: mapSeverity(f.Severity),
		Source:   "metrics",
		Title:    "Metric: " + f.Rule,
		Body:     fmt.Sprintf("value %.2f crossed the threshold", f.Value),
		Labels:   map[string]string{"rule": f.Rule},
		TS:       f.Time,
		DedupKey: key,
	}
}

// originOf normalizes a job Source string into a coarse origin label for rule
// routing. Catalogue of sources (closed): "user", "user:ai-analyze:<key>",
// "scheduler:<id>", "scheduler-manual:<id>".
func originOf(source string) string {
	switch {
	case strings.HasPrefix(source, "scheduler:"), strings.HasPrefix(source, "scheduler-manual:"):
		return "scheduler"
	case strings.HasPrefix(source, "user:ai-analyze:"):
		return "ai"
	default:
		return "user"
	}
}

// jobTitle builds a short PT-BR headline for the event.
func jobTitle(j *queue.Job) string {
	switch j.Status {
	case queue.StatusDone:
		return "Job completed: " + j.Kind
	case queue.StatusFailed:
		return "Job failed: " + j.Kind
	case queue.StatusCancelled:
		return "Job cancelled: " + j.Kind
	case queue.StatusInterrupted:
		return "Job interrupted: " + j.Kind
	default:
		return "Job " + string(j.Status) + ": " + j.Kind
	}
}

// seedNotifyDefaults makes the ticket functional on first run AND migrates the
// legacy single global WhatsApp destination (config.Alerting) into a channel +
// rule. It is conservative: it only acts on a PRISTINE config (zero channels and
// zero rules), so it never clobbers operator edits. If config.Alerting has no
// WhatsApp destination configured, it seeds nothing — the operator wires a
// channel in the Alertas tab instead (no half-baked state).
func (r *Router) seedNotifyDefaults() {
	if r.notify == nil {
		return
	}
	if len(r.notify.Rules()) > 0 || len(r.notify.ChannelDefs()) > 0 {
		return // already configured — respect existing state
	}

	r.cfgMu.Lock()
	al := r.cfg.Alerting
	r.cfgMu.Unlock()
	if al.FromUser == "" || al.ChatJID == "" {
		return // no legacy destination to migrate; nothing to seed
	}

	ch, err := r.notify.UpsertChannel(notify.ChannelDef{
		Name: "WhatsApp (migrated from alerting)", Type: wachannel.Type, Enabled: true,
		Config: notify.ChannelConfig{FromUser: al.FromUser, ChatJID: al.ChatJID},
	})
	if err != nil {
		log.Printf("notify seed: channel: %v", err)
		return
	}
	if _, err := r.notify.UpsertRule(notify.Rule{
		Name: "Jobs (failure and completion)", Enabled: true,
		TypePrefix: "job.", Channels: []string{ch.ID},
	}); err != nil {
		log.Printf("notify seed: rule: %v", err)
	}
}

// schedSourceID extracts the scheduler job id from a queue Source of the form
// "scheduler:<id>" or "scheduler-manual:<id>". Returns "" for chained or
// non-scheduler sources (so per-job notify only fires for the original run).
func schedSourceID(source string) string {
	if s, ok := strings.CutPrefix(source, "scheduler:"); ok {
		return s
	}
	if s, ok := strings.CutPrefix(source, "scheduler-manual:"); ok {
		return s
	}
	return ""
}
