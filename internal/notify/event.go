package notify

type Event struct {
	Type     string            `json:"type"`
	Severity string            `json:"severity"`
	Source   string            `json:"source"`
	Owner    string            `json:"owner,omitempty"`
	Title    string            `json:"title"`
	Body     string            `json:"body,omitempty"`
	Labels   map[string]string `json:"labels,omitempty"`
	TS       int64             `json:"ts"`
	DedupKey string            `json:"dedup_key,omitempty"`
	RuleID   string            `json:"rule_id,omitempty"`
}

const (
	TypeJobFailed              = "job.failed"
	TypeJobDone                = "job.done"
	TypeJobCancelled           = "job.cancelled"
	TypeJobInterrupted         = "job.interrupted"
	TypeMetricThreshold        = "metric.threshold"
	TypeMetricResolved         = "metric.resolved"
	TypeSchedulerEnqueueFailed = "scheduler.enqueue_failed"
	TypeAgentWaiting           = "agent.waiting_input"
	TypeAgentDone              = "agent.done"
	TypeDevicePairingPending   = "device.pairing_pending"
)

const (
	SeverityInfo     = "info"
	SeverityWarning  = "warning"
	SeverityCritical = "critical"
)

func severityRank(s string) int {
	switch s {
	case SeverityCritical:
		return 2
	case SeverityWarning:
		return 1
	default:
		return 0
	}
}
