// Package notify is the event-driven notification spine.
//
// It decouples *what happened* (an Event produced anywhere in the app: a job
// finished, a metric crossed a threshold, the scheduler refused an enqueue)
// from *where it goes* (a Channel: WhatsApp today; e-mail/webhook later).
// A Router matches events against user-defined Rules and fans them out to the
// configured Channels, with throttling, dedup, and a per-channel circuit
// breaker so a dead destination never stalls the producers.
//
// Design contract (see router.go for the full concurrency armor):
//
//   - Dispatch(ev) is NON-BLOCKING and never touches the caller's locks. A
//     job finishing under queue.mu can call it directly: history is written
//     under the Router's own histMu, then the event is handed to a buffered
//     channel (drop+count on overflow). A WAHA outage can never block the
//     metrics tick or a job's terminal path.
//   - All sending, matching, throttling and breaker state live in a single
//     worker goroutine (plus copy-on-write config under cfgMu). This is what
//     makes `go test -race` meaningful.
package notify

// Event is a single thing-that-happened, produced by any subsystem and routed
// by the Router. It is intentionally flat and self-describing so the history
// ring and the UI can render it without consulting the producer.
type Event struct {
	Type     string            `json:"type"`            // e.g. "job.failed", "metric.threshold"
	Severity string            `json:"severity"`        // info | warning | critical
	Source   string            `json:"source"`          // raw origin string, e.g. "scheduler:<id>", "user"
	Owner    string            `json:"owner,omitempty"` // user that owns the originating object
	Title    string            `json:"title"`           // short human headline
	Body     string            `json:"body,omitempty"`  // longer detail
	Labels   map[string]string `json:"labels,omitempty"`
	TS       int64             `json:"ts"`                  // unix seconds
	DedupKey string            `json:"dedup_key,omitempty"` // throttle/dedup identity (e.g. "job:<id>")
	// RuleID identifies which Rule matched to produce this particular Send
	// call. Router.handle() sets it on a per-rule copy of ev immediately
	// before sendVia — one ChannelDef can be the fan-out target of many
	// Rules, so this is the only way a Channel impl (namely PushChannel,
	// for the per-device filter) knows which Rule caused a given
	// delivery. Additive/omitempty: every existing Channel impl that
	// ignores unrecognized Event fields keeps compiling and behaving
	// identically.
	RuleID string `json:"rule_id,omitempty"`
}

// Event types. Producers SHOULD use these constants so rules built in the UI
// (which offers them as a catalog) line up with what actually fires.
const (
	TypeJobFailed              = "job.failed"
	TypeJobDone                = "job.done"
	TypeJobCancelled           = "job.cancelled"
	TypeJobInterrupted         = "job.interrupted"
	TypeMetricThreshold        = "metric.threshold"
	TypeMetricResolved         = "metric.resolved"
	TypeSchedulerEnqueueFailed = "scheduler.enqueue_failed"
	// Agent status events: a Claude Code hook reported the
	// agent is waiting for user input, or finished its turn.
	TypeAgentWaiting = "agent.waiting_input"
	TypeAgentDone    = "agent.done"
	// TypeDevicePairingPending fires when a device completes the
	// registration of a passkey through QR-code pairing, but the credential
	// is born in "pending" status (internal/auth.CredentialStatusPending) and
	// cannot log in on its own yet — this event is the only way the account
	// owner learns that an approval is waiting.
	TypeDevicePairingPending = "device.pairing_pending"
)

// Severity levels, ordered. MinSeverity matching uses severityRank.
const (
	SeverityInfo     = "info"
	SeverityWarning  = "warning"
	SeverityCritical = "critical"
)

// severityRank maps a severity string to a comparable rank. Unknown/empty
// sorts as the lowest (info) so a malformed event still routes through
// info-level rules rather than vanishing.
func severityRank(s string) int {
	switch s {
	case SeverityCritical:
		return 2
	case SeverityWarning:
		return 1
	default: // SeverityInfo and anything unrecognized
		return 0
	}
}
