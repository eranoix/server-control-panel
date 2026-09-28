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

const fcmServiceAccountSecret = "fcm_service_account"

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

func (r *Router) initNotify() {
	r.pushDevices = mobilebff.NewDeviceTokenStore(r.cfg.DataDir)
	r.pushDevicePrefs = mobilebff.NewDevicePrefsStore(r.cfg.DataDir)

	r.fcmSender = r.initFCMSender(r.pushDevices)

	rt, err := notify.New(notify.Options{
		DataDir: r.cfg.DataDir,
		Channels: map[string]notify.Channel{
			wachannel.Type:      wachannel.New(func() *whatsapp.Manager { return r.whatsappMgr }),
			notify.TypeTelegram: notify.NewTelegramChannel(),
			notify.TypeEmail:    notify.NewEmailChannel(),
			notify.TypeWebhook:  notify.NewWebhookChannel(),
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
	rt.AddChannelImpl(notify.TypeInApp, notify.NewInAppChannel(func(ev notify.Event) {
		rt.InboxAdd(ev)
		r.bridgeNotifyInboxToHub(ev)
	}))
	r.seedNotifyDefaults()

	if r.queue != nil {
		r.queue.SetNotifier(func(j *queue.Job) {
			r.notify.Dispatch(jobEvent(j))
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

func (r *Router) seedNotifyDefaults() {
	if r.notify == nil {
		return
	}
	if len(r.notify.Rules()) > 0 || len(r.notify.ChannelDefs()) > 0 {
		return
	}

	r.cfgMu.Lock()
	al := r.cfg.Alerting
	r.cfgMu.Unlock()
	if al.FromUser == "" || al.ChatJID == "" {
		return
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

func schedSourceID(source string) string {
	if s, ok := strings.CutPrefix(source, "scheduler:"); ok {
		return s
	}
	if s, ok := strings.CutPrefix(source, "scheduler-manual:"); ok {
		return s
	}
	return ""
}
