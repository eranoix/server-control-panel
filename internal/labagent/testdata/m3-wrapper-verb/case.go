package fixture

import "context"

// M3 — closes the residual risk: a wrapper of our OWN. There is no exec.Command at the call
// site; the verb comes from the request body. A naive detector sails right past.
func trainerRun(ctx context.Context, stdin []byte, args ...string) error { return nil }

type request struct {
	Verb string `json:"verb"`
}

func newOp(ctx context.Context, req request, body []byte) error {
	return trainerRun(ctx, body, req.Verb)
}
