package caso

import "context"

// trainerRun is the in-house wrapper: it executes a process without going through
// exec.Command at the call site. Anyone calling it with a verb that is not a
// literal is opening free execution under another name.
func trainerRun(ctx context.Context, stdin []byte, args ...string) error { return nil }

func Aplica(ctx context.Context, body []byte, verbo string) error {
	return trainerRun(ctx, body, verbo)
}
