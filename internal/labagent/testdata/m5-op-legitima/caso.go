package caso

import "context"

// M5 — MAIN NEGATIVE CONTROL: a new, legitimate, well-formed operation.
// A declared constant, the handler as a method expression, no new route, no
// argv. NO pin may fail this — otherwise the guard fails legitimate
// growth and someone switches it off.
type OpName string

const OpMundoArquivar OpName = "world.archive"

type Agent struct{}

func (a *Agent) opMundoArquivar(ctx context.Context, corpo []byte) (any, error) {
	return nil, nil
}
