// Package alert receives webhooks from Prometheus Alertmanager and dispatches
// notifications via WhatsApp using the configured user's WAHA client.
//
// Prerequisites:
//   - Alertmanager configured with a webhook receiver pointing to
//     http://127.0.0.1:8765/_internal/alert (loopback only).
//   - The FromUser user's WhatsApp account provisioned (Manager.Has(user) == true)
//     and Service.Client authenticated.
//   - Config.Alerting.Enabled = true + a valid ChatJID.
//
// The /_internal/alert endpoint validates that RemoteAddr is loopback (127.0.0.1
// or ::1) — Traefik does not route loopback addresses, so the only possible
// caller is Alertmanager running on the host (network_mode: host).
package alert

import "time"

// Payload is Alertmanager's webhook v4 format.
// Spec: https://prometheus.io/docs/alerting/latest/configuration/#webhook_config
type Payload struct {
	Version           string            `json:"version"`
	GroupKey          string            `json:"groupKey"`
	TruncatedAlerts   int               `json:"truncatedAlerts"`
	Status            string            `json:"status"` // "firing" | "resolved"
	Receiver          string            `json:"receiver"`
	GroupLabels       map[string]string `json:"groupLabels"`
	CommonLabels      map[string]string `json:"commonLabels"`
	CommonAnnotations map[string]string `json:"commonAnnotations"`
	ExternalURL       string            `json:"externalURL"`
	Alerts            []Alert           `json:"alerts"`
}

type Alert struct {
	Status       string            `json:"status"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     time.Time         `json:"startsAt"`
	EndsAt       time.Time         `json:"endsAt"`
	GeneratorURL string            `json:"generatorURL"`
	Fingerprint  string            `json:"fingerprint"`
}
