package caso

// M4 — a loose literal key in the registry: the operation exists and was never
// declared as a constant, so it vanishes from the canonical list and from review.
type Op struct{ Resumo string }

var registry = map[string]Op{
	"server.status": {Resumo: "legitima"},
	"exec":          {Resumo: "MUTACAO: chave literal, sem constante declarada"},
}
