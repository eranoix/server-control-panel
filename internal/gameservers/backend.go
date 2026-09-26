package gameservers

import (
	"context"
	"encoding/json"
	"io"
)

// Backend is the transport boundary for the game operations.
//
// There are two implementers: a LOCAL one (calls the Manager in the same
// process) and an HTTP one (talks to the node's node-agent). The panel chooses by
// `Node.transport` and does not know the difference.
//
// THE INTERFACE IS BORN BEFORE THE IMPLEMENTATION, on purpose: both sides are
// implemented against a contract that is already written, instead of discovering
// the shape by exploring the code — which is how the two sides drift apart.
//
// NO FILE SEMANTICS CROSSES THIS BOUNDARY
//
// No parameter and no result here is a `path`, `mode`, `uid`, `gid` or
// `os.FileMode`. The reason is not aesthetic: a host path that crosses the
// boundary is a path the client gets to choose, and the day the panel sends a
// path the agent stops being narrow without any new route having appeared. Owner
// and mode are resolved ON THE OTHER SIDE, by looking at the disk (see
// writeAtomic in fsatomic.go) — never negotiated by the protocol.
//
// This is machine-checked by TestBackendDoesNotLeakFileSemantics, which walks
// this file's AST. It is a pin, not good intentions.
//
// Where a path used to pass, a `Handle` passes now: an OPAQUE identifier, issued
// by the side that has the disk and resolved only by it.

// Handle is an opaque reference to an artifact living on the node (an exported
// world zip, a downloadable backup). The client receives it, keeps it and gives
// it back; it never interprets it. Changing the internal format breaks no client
// — and, more importantly, a client cannot FABRICATE a handle for an artifact it
// was never offered.
type Handle string

// Verb is the CLOSED set of lifecycle actions of server.action.
//
// Its own type instead of a free string: this is what stops `server.action` from
// turning into free execution on the inside. An unknown verb is refused by the
// recipient, and adding a verb requires adding a constant.
type Verb string

const (
	VerbStart   Verb = "start"
	VerbStop    Verb = "stop"
	VerbRestart Verb = "restart"
	// VerbUpdate is a restart with declared intent: the image runs steamcmd at
	// container start, so updating IS restarting. Keeping the verb apart keeps the
	// intent readable in the log and on screen, without inventing a new path.
	VerbUpdate Verb = "update"
)

// ValidVerbs is the closed set, so the recipient can refuse everything else.
var ValidVerbs = map[Verb]bool{
	VerbStart: true, VerbStop: true, VerbRestart: true, VerbUpdate: true,
}

// Backend executes named operations from the catalog.
//
// Execute's signature is deliberately poor: a catalog name and a JSON document.
// There is no `[]string` of argv, no `string` of command, no path. An operation
// that wanted to be free execution would have to CHANGE THIS SIGNATURE — and it
// is that change of shape that the pin detects. Searching for the string "exec"
// is trivially circumvented; requiring the shape is not.
type Backend interface {
	// Execute runs a catalog operation and returns the response document.
	// A name outside AllOps is refused by the recipient.
	Execute(ctx context.Context, op OpName, body json.RawMessage) (json.RawMessage, error)

	// Open returns the content of an artifact previously referenced by a Handle.
	// Separate from Execute because a stream does not fit in a JSON document —
	// and it is the only point where file bytes cross the boundary.
	// The caller closes.
	Open(ctx context.Context, h Handle) (io.ReadCloser, error)

	// Receive is the REVERSE path of Open: the client hands over bytes and gets
	// back a Handle to reference them in a later operation (world.import is the case).
	//
	// It takes no file name and no path — the world's name travels in the
	// operation envelope, and the recipient chooses where to put the bytes.
	// Without this, "import world" would require the panel to know the node's
	// disk, which is exactly what this boundary forbids.
	//
	// Added afterwards, closing the gap the first implementation had declared.
	Receive(ctx context.Context, r io.Reader) (Handle, error)

	// Describe identifies the recipient, for diagnostics and for the screen.
	Describe() string
}

// ERROR CLASSES OF THE CONTRACT
//
// The two back-ends have to fail the SAME way, otherwise the parity is only on
// the happy path — and the happy path was never what diverges. In particular: an
// AUTHORIZATION failure must not disguise itself as "the operation failed". They
// ask for opposite actions from the operator: one is to swap the token, the
// other is to go look at the node.
//
// These declarations live HERE, and not in backend_local.go, because they are
// contract — they belong to the interface, not to one implementer.

// ErrHandleInvalid is the single answer for a handle that does not exist, has
// expired or belongs to another server. See why the message is single in
// handles.go:resolver. Sentinels are `const`, not `var`: errorHandle is a string
// type, so the constant is a compile-time literal and NOBODY can reassign it at
// runtime. An error sentinel in a `var` is reassignable global state — and the
// rule is "no new global state", measured by grep.
type errorHandle string

const (
	ErrHandleInvalid errorHandle = "handle is invalid, expired or from another server"

	// ErrTrainerMissing separates "there is no trainer on this machine" from "the
	// trainer failed" — the old handler already returned 404 here, and it survives.
	ErrTrainerMissing errorHandle = "trainer is not installed on this machine"
)

func (e errorHandle) Error() string { return string(e) }

// AuthorizationError is 401/403 from the node: the credential does not work. Its own
// type so `errors.As` can tell it apart without matching a message substring.
type AuthorizationError struct{ Msg string }

func (e *AuthorizationError) Error() string { return e.Msg }

// UnknownOperationError is 404 from the node: the name is not in THAT agent's
// catalog. Separate from OperationError because it means an incompatible version
// between panel and node, not a defect in the operation.
type UnknownOperationError struct{ Op OpName }

func (e *UnknownOperationError) Error() string {
	return "unknown operation on node: " + string(e.Op)
}

// OperationError is the failure of the operation itself, with the message the node gave.
type OperationError struct{ Msg string }

func (e *OperationError) Error() string { return e.Msg }
