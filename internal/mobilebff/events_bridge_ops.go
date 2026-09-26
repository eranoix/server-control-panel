package mobilebff

// events_bridge_ops.go is the ONLY egress from internal/queue's self_deploy
// job, and from the ops-status aggregation, into /ws/mobile-events (Plan
// 06-01's Hub) — no second subscription or notification mechanism is built
// alongside it.
import (
	"context"
	"encoding/json"
	"time"

	"server-control-panel/internal/httpx"
	"server-control-panel/internal/queue"
)

// bridgeSelfDeployJob subscribes to a just-enqueued self_deploy job's queue
// events and forwards each one onto the Hub's "deploy.<jobID>" channel,
// visible only to the job's owner or an admin — the same ownership rule
// handleQueueWS already enforces (internal/api/handlers_queue.go), not
// loosened for the mobile path. The forwarding goroutine stops (and
// unsubscribes) right after the job's terminal status event, so it never
// outlives the job; a self_deploy job is bounded by agentctl's own
// `flock -w 600` plus the deploy itself, so this is not unbounded.
//
// No-op when deps.Hub or deps.Queue is nil (Hub not wired yet, or the queue
// failed to start at boot) — the HTTP trigger/status endpoints
// (ops_deploy.go) still work without the live channel.
func bridgeSelfDeployJob(deps Deps, jobID, owner string) {
	if deps.Hub == nil || deps.Queue == nil {
		return
	}
	ch, unsubscribe := deps.Queue.Subscribe(jobID)
	channel := "deploy." + jobID
	visible := func(user string) bool {
		return user == owner || httpx.IsAdmin(deps.Cfg, user)
	}
	go func() {
		defer unsubscribe()
		for ev := range ch {
			data, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			deps.Hub.Publish(channel, Envelope{Type: ev.Type, Data: data}, visible)
			if isTerminalQueueEvent(ev) {
				return
			}
		}
	}()
}

// isTerminalQueueEvent reports whether ev is the job's terminal status
// transition — the "distinct final message" the contract requires, as opposed to
// the log/progress just stopping silently.
func isTerminalQueueEvent(ev queue.Event) bool {
	if ev.Type != "status" {
		return false
	}
	switch ev.Status {
	case queue.StatusDone, queue.StatusFailed, queue.StatusCancelled, queue.StatusInterrupted:
		return true
	default:
		return false
	}
}

// opsHealthTickInterval is how often the ops.health channel refreshes while
// at least one connection is subscribed to it — cheap enough for a
// dashboard, per RESEARCH.md's fan-out cost note about batching/throttling
// server-side. A var (not a const) so tests can shrink it instead of
// sleeping 12s per assertion.
var opsHealthTickInterval = 12 * time.Second

// StartOpsHealthPublisher starts the periodic ops.health publisher
// and returns a stop function; the caller (internal/api.Router.Shutdown)
// must call it exactly once on server shutdown, alongside the queue/notify/
// videocall shutdown sequence it already runs.
//
// A nil deps.Hub yields a no-op stop and starts no goroutine at all — so
// cmd/mobile-openapi-gen's empty Deps{}, and any caller that hasn't wired
// the Hub yet, stay completely inert.
func StartOpsHealthPublisher(deps Deps) (stop func()) {
	if deps.Hub == nil {
		return func() {}
	}
	ticker := time.NewTicker(opsHealthTickInterval)
	done := make(chan struct{})
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				// Nobody's listening: skip computing OpsStatus entirely —
				// health checks, queue counts and the alert scan all cost
				// real work (T-06 risk: wasted resources at scale).
				if !deps.Hub.HasSubscriber("ops.health") {
					continue
				}
				// A short deadline of its own: the resource collection (deps.SysStats,
				// see ops_metrics.go) is the only part of buildOpsStatus that can
				// block, and a tick must never outlast the interval to the next one.
				// Blowing the deadline makes the collection return an error, the
				// `system` field drops out of that envelope, and the next tick tries
				// again — the publisher never wedges.
				ctx, cancel := context.WithTimeout(context.Background(), opsHealthTickInterval)
				status := buildOpsStatus(ctx, deps)
				cancel()
				data, err := json.Marshal(status)
				if err != nil {
					continue
				}
				deps.Hub.Publish("ops.health", Envelope{Type: "ops.status", Data: data}, func(user string) bool {
					return httpx.IsAdmin(deps.Cfg, user)
				})
			}
		}
	}()
	return func() { close(done) }
}
