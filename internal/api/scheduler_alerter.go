package api

import (
	"log"
	"time"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/notify"
	"server-control-panel/internal/scheduler"
)

func (r *Router) schedulerAlerter() scheduler.Alerter {
	return func(owner string, j *scheduler.Job, queueJobID, status, lastErr string) {
		target := "name=" + j.Name + " status=" + status
		if queueJobID != "" {
			target += " queue=" + queueJobID
		}
		if lastErr != "" {
			target += " err=" + lastErr
		}
		log.Printf("[scheduler.alert] owner=%s %s", owner, target)
		if r.audit != nil {
			r.audit.Append(auth.Event{
				User:   owner,
				Action: "scheduler.alert",
				Target: target,
			})
		}
		if status == "failed" && r.notify != nil {
			r.notify.Dispatch(notify.Event{
				Type:     notify.TypeSchedulerEnqueueFailed,
				Severity: notify.SeverityCritical,
				Source:   "scheduler:" + j.ID,
				Owner:    owner,
				Title:    "Schedule failed to enqueue: " + j.Name,
				Body:     lastErr,
				Labels: map[string]string{
					"sched_job": j.Name,
					"sched_id":  j.ID,
					"origin":    "scheduler",
				},
				TS:       time.Now().Unix(),
				DedupKey: "sched-enqfail:" + j.ID,
			})
		}
	}
}
