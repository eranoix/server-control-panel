// Package labagent is the node agent: a CLOSED catalogue of named operations
// that runs ON THE NODE, behind a per-node bearer and a bind the network cannot reach.
//
// # THE JUSTIFICATION BY SHAPE, which is the heart of this design
//
// The project's own rules forbid the free-execution route in capital letters,
// and the prohibition is easy to write and hard to hold up: a free-execution
// endpoint does not have to carry the obvious name. It can call itself
// `manutencao.rodar` and take a `[]string`.
//
// (The literal name of the forbidden route does NOT appear in this file, on
// purpose: there is an absence invariant over it, and the gate matches by fixed
// text without discounting comments — writing it, even in order to forbid it,
// would fail the deploy. That is how this pin bit its own author.)
//
// So the defence here is not the NAME but the SHAPE:
//
//	type Handler func(*Agent, context.Context, json.RawMessage) (any, error)
//
// This signature HAS NOWHERE to take argv or a command. An operation that wanted
// to be free execution would have to change the signature, add a route outside
// the map, or swap the method expression for a closure capturing a free
// argument — and all three changes are STRUCTURAL, detectable by AST.
// That is what the mutation tests mutate and prove. Searching for the string
// "exec" would be worked around by anyone who cared to; demanding the shape, not.
package labagent

import (
	"context"
	"encoding/json"

	"server-control-panel/internal/gameservers"
)

// Handler runs one operation. See the justification by shape in the header:
// `json.RawMessage` in and `any` out are the whole contract.
type Handler func(*Agent, context.Context, json.RawMessage) (any, error)

// Op is the catalogue entry.
type Op struct {
	// Handler is ALWAYS a method expression on *Agent — `(*Agent).opFoo` —
	// never a function literal. A function literal can capture a variable from
	// the enclosing scope, and capture is exactly how a free argument would get
	// in without ever showing up in the signature.
	Handler Handler

	// Muta says whether the operation changes state. Used by the log and by the
	// write audit; declared here so the answer is a property of the operation
	// and not of the caller.
	Muta bool

	// Resumo is the one-line text the screen shows.
	Resumo string
}

// registry is the CLOSED catalogue.
//
// A package `var`, and not a constructor function, for two concrete reasons: it
// is what the positive invariant can assert by grep, and it is what the
// exhaustiveness test can walk without executing anything.
//
// Every entry here has to have a matching constant in
// gameservers.TodasAsOps, and vice versa. Both sides are demanded by tests —
// one without the other is an invisible operation.
var registry = map[gameservers.OpName]Op{
	// ── server ────────────────────────────────────────────────────────────────
	gameservers.OpServerList:   {Handler: (*Agent).opServerList, Muta: false, Resumo: "inventory of the node's servers"},
	gameservers.OpServerStatus: {Handler: (*Agent).opServerStatus, Muta: false, Resumo: "state, connection and build in a single lookup"},
	gameservers.OpServerAction: {Handler: (*Agent).opServerAction, Muta: true, Resumo: "start, stop, restart or update"},
	gameservers.OpServerLogs:   {Handler: (*Agent).opServerLogs, Muta: false, Resumo: "tail of the container logs"},

	// ── world ─────────────────────────────────────────────────────────────────
	gameservers.OpWorldList:      {Handler: (*Agent).opWorldList, Muta: false, Resumo: "the server's worlds"},
	gameservers.OpWorldSwitch:    {Handler: (*Agent).opWorldSwitch, Muta: true, Resumo: "switch the active world"},
	gameservers.OpWorldExport:    {Handler: (*Agent).opWorldExport, Muta: false, Resumo: "pack a world and return a Handle"},
	gameservers.OpWorldImport:    {Handler: (*Agent).opWorldImport, Muta: true, Resumo: "install a world from a Handle"},
	gameservers.OpWorldRename:    {Handler: (*Agent).opWorldRename, Muta: true, Resumo: "rename the world's folder"},
	gameservers.OpWorldDuplicate: {Handler: (*Agent).opWorldDuplicate, Muta: true, Resumo: "copy a world"},
	gameservers.OpWorldDelete:    {Handler: (*Agent).opWorldDelete, Muta: true, Resumo: "delete a world"},

	// ── settings ──────────────────────────────────────────────────────────────
	gameservers.OpSettingsGet:   {Handler: (*Agent).opSettingsGet, Muta: false, Resumo: "game config, including groups and bans"},
	gameservers.OpSettingsPatch: {Handler: (*Agent).opSettingsPatch, Muta: true, Resumo: "change enumerated config fields"},

	// ── runtime ───────────────────────────────────────────────────────────────
	gameservers.OpRuntimeGet:   {Handler: (*Agent).opRuntimeGet, Muta: false, Resumo: "compose env"},
	gameservers.OpRuntimePatch: {Handler: (*Agent).opRuntimePatch, Muta: true, Resumo: "change the compose env and recreate the container"},

	// ── backup ────────────────────────────────────────────────────────────────
	gameservers.OpBackupList:     {Handler: (*Agent).opBackupList, Muta: false, Resumo: "available backups"},
	gameservers.OpBackupCreate:   {Handler: (*Agent).opBackupCreate, Muta: true, Resumo: "create a backup"},
	gameservers.OpBackupRestore:  {Handler: (*Agent).opBackupRestore, Muta: true, Resumo: "restore a backup"},
	gameservers.OpBackupDownload: {Handler: (*Agent).opBackupDownload, Muta: false, Resumo: "return a Handle to download a backup"},

	// ── trainer ───────────────────────────────────────────────────────────────
	gameservers.OpTrainerStatus:  {Handler: (*Agent).opTrainerStatus, Muta: false, Resumo: "raw snapshot from tl-agent"},
	gameservers.OpTrainerApply:   {Handler: (*Agent).opTrainerApply, Muta: true, Resumo: "apply the desired state to the live process"},
	gameservers.OpTrainerDesired: {Handler: (*Agent).opTrainerDesired, Muta: true, Resumo: "store the desired state"},

	// ── history ───────────────────────────────────────────────────────────────
	gameservers.OpHistoryList: {Handler: (*Agent).opHistoryList, Muta: false, Resumo: "telemetry samples"},
}

// Registro returns the operation, and whether it exists. A name outside the
// catalogue is NOT a generic error: the caller returns 404, so that "does not
// exist" and "failed" never get confused in a diagnosis.
func Registro(op gameservers.OpName) (Op, bool) {
	o, ok := registry[op]
	return o, ok
}
