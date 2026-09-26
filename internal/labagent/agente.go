package labagent

import (
	"context"
	"encoding/json"
	"fmt"

	"server-control-panel/internal/gameservers"
)

// Agent is the agent's state. Only what is needed to serve the catalogue.
//
// NO HANDLER BELOW CONTAINS FILE LOGIC, and that is not a saving in typing: if
// one did, the later extraction would be born already undone — the logic would
// have two owners and one day the two would diverge. Here the handlers only
// validate the shape of the document and delegate to the Backend.
type Agent struct {
	// No is the node's name, for diagnostics and for /metrics.
	No string

	// Back is what actually executes. In the agent it is the LOCAL back-end;
	// in a test it is a double. The Agent does not know the difference — it is
	// the same design trainer.go already uses, validated in production.
	Back gameservers.Backend
}

// delega is the common body: all 23 handlers are the same sentence.
//
// Having ONE delegation function, instead of 23 look-alike bodies, is what
// keeps a handler from picking up logic of its own by accident — there is
// nowhere to put it.
func (a *Agent) delega(ctx context.Context, op gameservers.OpName, corpo json.RawMessage) (any, error) {
	if a.Back == nil {
		return nil, fmt.Errorf("agent with no back-end configured")
	}
	return a.Back.Executar(ctx, op, corpo)
}

// ── server ───────────────────────────────────────────────────────────────────

func (a *Agent) opServerList(ctx context.Context, c json.RawMessage) (any, error) {
	return a.delega(ctx, gameservers.OpServerList, c)
}
func (a *Agent) opServerStatus(ctx context.Context, c json.RawMessage) (any, error) {
	return a.delega(ctx, gameservers.OpServerStatus, c)
}
func (a *Agent) opServerAction(ctx context.Context, c json.RawMessage) (any, error) {
	return a.delega(ctx, gameservers.OpServerAction, c)
}
func (a *Agent) opServerLogs(ctx context.Context, c json.RawMessage) (any, error) {
	return a.delega(ctx, gameservers.OpServerLogs, c)
}

// ── world ────────────────────────────────────────────────────────────────────

func (a *Agent) opWorldList(ctx context.Context, c json.RawMessage) (any, error) {
	return a.delega(ctx, gameservers.OpWorldList, c)
}
func (a *Agent) opWorldSwitch(ctx context.Context, c json.RawMessage) (any, error) {
	return a.delega(ctx, gameservers.OpWorldSwitch, c)
}
func (a *Agent) opWorldExport(ctx context.Context, c json.RawMessage) (any, error) {
	return a.delega(ctx, gameservers.OpWorldExport, c)
}
func (a *Agent) opWorldImport(ctx context.Context, c json.RawMessage) (any, error) {
	return a.delega(ctx, gameservers.OpWorldImport, c)
}
func (a *Agent) opWorldRename(ctx context.Context, c json.RawMessage) (any, error) {
	return a.delega(ctx, gameservers.OpWorldRename, c)
}
func (a *Agent) opWorldDuplicate(ctx context.Context, c json.RawMessage) (any, error) {
	return a.delega(ctx, gameservers.OpWorldDuplicate, c)
}
func (a *Agent) opWorldDelete(ctx context.Context, c json.RawMessage) (any, error) {
	return a.delega(ctx, gameservers.OpWorldDelete, c)
}

// ── settings ─────────────────────────────────────────────────────────────────

func (a *Agent) opSettingsGet(ctx context.Context, c json.RawMessage) (any, error) {
	return a.delega(ctx, gameservers.OpSettingsGet, c)
}
func (a *Agent) opSettingsPatch(ctx context.Context, c json.RawMessage) (any, error) {
	return a.delega(ctx, gameservers.OpSettingsPatch, c)
}

// ── runtime ──────────────────────────────────────────────────────────────────

func (a *Agent) opRuntimeGet(ctx context.Context, c json.RawMessage) (any, error) {
	return a.delega(ctx, gameservers.OpRuntimeGet, c)
}
func (a *Agent) opRuntimePatch(ctx context.Context, c json.RawMessage) (any, error) {
	return a.delega(ctx, gameservers.OpRuntimePatch, c)
}

// ── backup ───────────────────────────────────────────────────────────────────

func (a *Agent) opBackupList(ctx context.Context, c json.RawMessage) (any, error) {
	return a.delega(ctx, gameservers.OpBackupList, c)
}
func (a *Agent) opBackupCreate(ctx context.Context, c json.RawMessage) (any, error) {
	return a.delega(ctx, gameservers.OpBackupCreate, c)
}
func (a *Agent) opBackupRestore(ctx context.Context, c json.RawMessage) (any, error) {
	return a.delega(ctx, gameservers.OpBackupRestore, c)
}
func (a *Agent) opBackupDownload(ctx context.Context, c json.RawMessage) (any, error) {
	return a.delega(ctx, gameservers.OpBackupDownload, c)
}

// ── trainer ──────────────────────────────────────────────────────────────────

func (a *Agent) opTrainerStatus(ctx context.Context, c json.RawMessage) (any, error) {
	return a.delega(ctx, gameservers.OpTrainerStatus, c)
}
func (a *Agent) opTrainerApply(ctx context.Context, c json.RawMessage) (any, error) {
	return a.delega(ctx, gameservers.OpTrainerApply, c)
}
func (a *Agent) opTrainerDesired(ctx context.Context, c json.RawMessage) (any, error) {
	return a.delega(ctx, gameservers.OpTrainerDesired, c)
}

// ── history ──────────────────────────────────────────────────────────────────

func (a *Agent) opHistoryList(ctx context.Context, c json.RawMessage) (any, error) {
	return a.delega(ctx, gameservers.OpHistoryList, c)
}
