package gameservers

type OpName string

const (
	OpServerList   OpName = "server.list"
	OpServerStatus OpName = "server.status"
	OpServerAction OpName = "server.action"
	OpServerLogs   OpName = "server.logs"

	OpWorldList      OpName = "world.list"
	OpWorldSwitch    OpName = "world.switch"
	OpWorldExport    OpName = "world.export"
	OpWorldImport    OpName = "world.import"
	OpWorldRename    OpName = "world.rename"
	OpWorldDuplicate OpName = "world.duplicate"
	OpWorldDelete    OpName = "world.delete"

	OpSettingsGet   OpName = "settings.get"
	OpSettingsPatch OpName = "settings.patch"

	OpRuntimeGet   OpName = "runtime.get"
	OpRuntimePatch OpName = "runtime.patch"

	OpBackupList     OpName = "backup.list"
	OpBackupCreate   OpName = "backup.create"
	OpBackupRestore  OpName = "backup.restore"
	OpBackupDownload OpName = "backup.download"

	OpTrainerStatus  OpName = "trainer.status"
	OpTrainerApply   OpName = "trainer.apply"
	OpTrainerDesired OpName = "trainer.desired"

	OpHistoryList OpName = "history.list"
)

var ValidFamilies = map[string]bool{
	"server":   true,
	"world":    true,
	"settings": true,
	"runtime":  true,
	"backup":   true,
	"trainer":  true,
	"history":  true,
}

var AllOps = []OpName{
	OpServerList, OpServerStatus, OpServerAction, OpServerLogs,
	OpWorldList, OpWorldSwitch, OpWorldExport, OpWorldImport,
	OpWorldRename, OpWorldDuplicate, OpWorldDelete,
	OpSettingsGet, OpSettingsPatch,
	OpRuntimeGet, OpRuntimePatch,
	OpBackupList, OpBackupCreate, OpBackupRestore, OpBackupDownload,
	OpTrainerStatus, OpTrainerApply, OpTrainerDesired,
	OpHistoryList,
}
