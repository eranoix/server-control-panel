package scheduler

import (
	"encoding/json"

	"server-control-panel/internal/queue"
)

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
