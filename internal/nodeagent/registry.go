package nodeagent

import (
	"context"
	"encoding/json"

	"server-control-panel/internal/gameservers"
)

type Handler func(*Agent, context.Context, json.RawMessage) (any, error)

type Op struct {
	Handler Handler

	Mutates bool

	Summary string
}

var registry = map[gameservers.OpName]Op{
	gameservers.OpServerList:   {Handler: (*Agent).opServerList, Mutates: false, Summary: "inventory of the node's servers"},
	gameservers.OpServerStatus: {Handler: (*Agent).opServerStatus, Mutates: false, Summary: "state, connection and build in a single lookup"},
	gameservers.OpServerAction: {Handler: (*Agent).opServerAction, Mutates: true, Summary: "start, stop, restart or update"},
	gameservers.OpServerLogs:   {Handler: (*Agent).opServerLogs, Mutates: false, Summary: "tail of the container logs"},

	gameservers.OpWorldList:      {Handler: (*Agent).opWorldList, Mutates: false, Summary: "the server's worlds"},
	gameservers.OpWorldSwitch:    {Handler: (*Agent).opWorldSwitch, Mutates: true, Summary: "switch the active world"},
	gameservers.OpWorldExport:    {Handler: (*Agent).opWorldExport, Mutates: false, Summary: "pack a world and return a Handle"},
	gameservers.OpWorldImport:    {Handler: (*Agent).opWorldImport, Mutates: true, Summary: "install a world from a Handle"},
	gameservers.OpWorldRename:    {Handler: (*Agent).opWorldRename, Mutates: true, Summary: "rename the world's folder"},
	gameservers.OpWorldDuplicate: {Handler: (*Agent).opWorldDuplicate, Mutates: true, Summary: "copy a world"},
	gameservers.OpWorldDelete:    {Handler: (*Agent).opWorldDelete, Mutates: true, Summary: "delete a world"},

	gameservers.OpSettingsGet:   {Handler: (*Agent).opSettingsGet, Mutates: false, Summary: "game config, including groups and bans"},
	gameservers.OpSettingsPatch: {Handler: (*Agent).opSettingsPatch, Mutates: true, Summary: "change enumerated config fields"},

	gameservers.OpRuntimeGet:   {Handler: (*Agent).opRuntimeGet, Mutates: false, Summary: "compose env"},
	gameservers.OpRuntimePatch: {Handler: (*Agent).opRuntimePatch, Mutates: true, Summary: "change the compose env and recreate the container"},

	gameservers.OpBackupList:     {Handler: (*Agent).opBackupList, Mutates: false, Summary: "available backups"},
	gameservers.OpBackupCreate:   {Handler: (*Agent).opBackupCreate, Mutates: true, Summary: "create a backup"},
	gameservers.OpBackupRestore:  {Handler: (*Agent).opBackupRestore, Mutates: true, Summary: "restore a backup"},
	gameservers.OpBackupDownload: {Handler: (*Agent).opBackupDownload, Mutates: false, Summary: "return a Handle to download a backup"},

	gameservers.OpTrainerStatus:  {Handler: (*Agent).opTrainerStatus, Mutates: false, Summary: "raw snapshot from tl-agent"},
	gameservers.OpTrainerApply:   {Handler: (*Agent).opTrainerApply, Mutates: true, Summary: "apply the desired state to the live process"},
	gameservers.OpTrainerDesired: {Handler: (*Agent).opTrainerDesired, Mutates: true, Summary: "store the desired state"},

	gameservers.OpHistoryList: {Handler: (*Agent).opHistoryList, Mutates: false, Summary: "telemetry samples"},
}

func Lookup(op gameservers.OpName) (Op, bool) {
	o, ok := registry[op]
	return o, ok
}
