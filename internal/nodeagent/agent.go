package nodeagent

import (
	"context"
	"encoding/json"
	"fmt"

	"server-control-panel/internal/gameservers"
)

type Agent struct {
	No string

	Back gameservers.Backend
}

func (a *Agent) delegate(ctx context.Context, op gameservers.OpName, body json.RawMessage) (any, error) {
	if a.Back == nil {
		return nil, fmt.Errorf("agent with no back-end configured")
	}
	return a.Back.Execute(ctx, op, body)
}

func (a *Agent) opServerList(ctx context.Context, c json.RawMessage) (any, error) {
	return a.delegate(ctx, gameservers.OpServerList, c)
}
func (a *Agent) opServerStatus(ctx context.Context, c json.RawMessage) (any, error) {
	return a.delegate(ctx, gameservers.OpServerStatus, c)
}
func (a *Agent) opServerAction(ctx context.Context, c json.RawMessage) (any, error) {
	return a.delegate(ctx, gameservers.OpServerAction, c)
}
func (a *Agent) opServerLogs(ctx context.Context, c json.RawMessage) (any, error) {
	return a.delegate(ctx, gameservers.OpServerLogs, c)
}

func (a *Agent) opWorldList(ctx context.Context, c json.RawMessage) (any, error) {
	return a.delegate(ctx, gameservers.OpWorldList, c)
}
func (a *Agent) opWorldSwitch(ctx context.Context, c json.RawMessage) (any, error) {
	return a.delegate(ctx, gameservers.OpWorldSwitch, c)
}
func (a *Agent) opWorldExport(ctx context.Context, c json.RawMessage) (any, error) {
	return a.delegate(ctx, gameservers.OpWorldExport, c)
}
func (a *Agent) opWorldImport(ctx context.Context, c json.RawMessage) (any, error) {
	return a.delegate(ctx, gameservers.OpWorldImport, c)
}
func (a *Agent) opWorldRename(ctx context.Context, c json.RawMessage) (any, error) {
	return a.delegate(ctx, gameservers.OpWorldRename, c)
}
func (a *Agent) opWorldDuplicate(ctx context.Context, c json.RawMessage) (any, error) {
	return a.delegate(ctx, gameservers.OpWorldDuplicate, c)
}
func (a *Agent) opWorldDelete(ctx context.Context, c json.RawMessage) (any, error) {
	return a.delegate(ctx, gameservers.OpWorldDelete, c)
}

func (a *Agent) opSettingsGet(ctx context.Context, c json.RawMessage) (any, error) {
	return a.delegate(ctx, gameservers.OpSettingsGet, c)
}
func (a *Agent) opSettingsPatch(ctx context.Context, c json.RawMessage) (any, error) {
	return a.delegate(ctx, gameservers.OpSettingsPatch, c)
}

func (a *Agent) opRuntimeGet(ctx context.Context, c json.RawMessage) (any, error) {
	return a.delegate(ctx, gameservers.OpRuntimeGet, c)
}
func (a *Agent) opRuntimePatch(ctx context.Context, c json.RawMessage) (any, error) {
	return a.delegate(ctx, gameservers.OpRuntimePatch, c)
}

func (a *Agent) opBackupList(ctx context.Context, c json.RawMessage) (any, error) {
	return a.delegate(ctx, gameservers.OpBackupList, c)
}
func (a *Agent) opBackupCreate(ctx context.Context, c json.RawMessage) (any, error) {
	return a.delegate(ctx, gameservers.OpBackupCreate, c)
}
func (a *Agent) opBackupRestore(ctx context.Context, c json.RawMessage) (any, error) {
	return a.delegate(ctx, gameservers.OpBackupRestore, c)
}
func (a *Agent) opBackupDownload(ctx context.Context, c json.RawMessage) (any, error) {
	return a.delegate(ctx, gameservers.OpBackupDownload, c)
}

func (a *Agent) opTrainerStatus(ctx context.Context, c json.RawMessage) (any, error) {
	return a.delegate(ctx, gameservers.OpTrainerStatus, c)
}
func (a *Agent) opTrainerApply(ctx context.Context, c json.RawMessage) (any, error) {
	return a.delegate(ctx, gameservers.OpTrainerApply, c)
}
func (a *Agent) opTrainerDesired(ctx context.Context, c json.RawMessage) (any, error) {
	return a.delegate(ctx, gameservers.OpTrainerDesired, c)
}

func (a *Agent) opHistoryList(ctx context.Context, c json.RawMessage) (any, error) {
	return a.delegate(ctx, gameservers.OpHistoryList, c)
}
