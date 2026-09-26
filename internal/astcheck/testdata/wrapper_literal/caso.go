package caso

import "context"

func trainerRun(ctx context.Context, stdin []byte, args ...string) error { return nil }

// Legitimate use of the same wrapper: the verb is a literal, the catalog stays closed.
func Aplica(ctx context.Context, body []byte) error {
	return trainerRun(ctx, body, "apply")
}
