package scheduler

import (
	"encoding/json"

	"server-control-panel/internal/queue"
)

// QueueEnqueuer adapts *queue.Queue to the scheduler's Enqueuer interface.
// Stays in its own file so the test suite can ignore the queue dependency
// by providing a fake Enqueuer in scheduler_test.go.
type QueueEnqueuer struct {
	Q *queue.Queue
}

func (q QueueEnqueuer) Enqueue(kind string, args json.RawMessage, owner, source string) (string, error) {
	j, err := q.Q.Enqueue(kind, args, owner, source)
	if err != nil {
		return "", err
	}
	return j.ID, nil
}
