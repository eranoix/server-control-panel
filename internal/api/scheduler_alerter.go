// scheduler_alerter.go — bridge between scheduler events and the operator's
// notification channels.
//
// Behaviour: still writes an audit event so failures show up in
// /audit and the topbar can surface them, AND routes enqueue FAILURES into the
// event-driven notification spine (internal/notify) so they reach configured
// channels (WhatsApp, …) by rule.
//
// Why only failures here: a scheduled job's *execution* result already flows
// through the queue terminal hook (scheduler.fire enqueues into the SAME queue,
// so a finished scheduled job carries Source "scheduler:<id>" / "origin":
// "scheduler" into jobEvent). This alerter is therefore reduced to the one
// signal the queue can't give us — the rare case where the scheduler couldn't
// even submit the job (enqueue refused). Notifying on enqueue-success would be
// premature and duplicate the queue's terminal event.
//
// The per-job scheduler.AlertOn knob is now VESTIGIAL: routing/silencing lives
// in notify rules. AlertOn is kept only for retro-compat of existing scheduled
// jobs and still gates whether this alerter is invoked at all.
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
		// Route enqueue FAILURES (status=="failed", queueJobID empty) into the
		// notify spine. Success ("ok") is deliberately NOT dispatched — see the
		// file header.
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
