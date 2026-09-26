package caso

import "context"

// M3 — closes the residual risk: a wrapper of our OWN. There is no exec.Command at the call
// site; the verb comes from the request body. A naive detector sails right past.
func trainerRun(ctx context.Context, stdin []byte, args ...string) error { return nil }

type pedido struct {
	Verbo string `json:"verbo"`
}

func opNova(ctx context.Context, req pedido, body []byte) error {
	return trainerRun(ctx, body, req.Verbo)
}
