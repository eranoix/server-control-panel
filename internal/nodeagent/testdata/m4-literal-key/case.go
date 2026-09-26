package fixture

// M4 — a loose literal key in the registry: the operation exists and was never
// declared as a constant, so it vanishes from the canonical list and from review.
type Op struct{ Summary string }

var registry = map[string]Op{
	"server.status": {Summary: "legitimate"},
	"exec":          {Summary: "MUTATION: literal key, no declared constant"},
}
