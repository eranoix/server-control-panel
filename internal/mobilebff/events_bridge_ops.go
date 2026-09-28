package mobilebff

import (
	"context"
	"encoding/json"
	"time"

	"server-control-panel/internal/httpx"
	"server-control-panel/internal/queue"
)

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

var opsHealthTickInterval = 12 * time.Second

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
				if !deps.Hub.HasSubscriber("ops.health") {
					continue
				}
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
