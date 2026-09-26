package gameservers

// CLOSED catalog of the lab-agent's named operations.
//
// WHY THE CONSTANTS LIVE HERE, AND NOT IN `labagent`
//
// The panel's HTTP back-end needs these names to build the request, and
// `gameservers` cannot import `labagent` without creating an import cycle. So
// the shared vocabulary lives in the lower package. This is NOT an accident of
// organization — if a future session "tidies it up" by moving it into
// `labagent`, the cycle shows up on the spot.
//
// ─────────────────────────────────────────────────────────────────────────────
// TRIAGE: the 27 `case` arms of internal/api/handlers_gameservers.go
//
// Every line of the original file has its destination declared below, INCLUDING
// those that stop being a route and become a field. Without this table, the next
// session cannot tell if a `case` was converted, absorbed or forgotten.
//
//	line   case                    destination
//	─────  ──────────────────────  ────────────────────────────────────────────
//	  72   action                  server.action
//	  84   start|stop|restart      value of Verbo in server.action (not a route)
//	  95   connection              FIELD of server.status
//	  98   groups                  section of settings.get / settings.patch
//	 131   bans                    section of settings.get / settings.patch
//	 157   history                 history.list
//	 166   build                   FIELD of server.status
//	 169   update                  value of Verbo in server.action
//	 179   rawconfig               settings.get (read) / settings.patch (write)
//	 209   trainer                 trainer.status / trainer.apply / trainer.desired
//	 254   runtime                 runtime.get / runtime.patch
//	 280   server                  settings.get / settings.patch (server section)
//	 320   worlds                  world family
//	 322     ""                    world.list
//	 333     switch                world.switch
//	 350     export                world.export
//	 362     import                world.import
//	 400     rename                world.rename
//	 417     duplicate             world.duplicate
//	 434     delete                world.delete
//	 455   settings                settings.get / settings.patch
//	 498   backups                 backup family
//	 500     ""                    backup.list
//	 511     create                backup.create
//	 522     restore               backup.restore
//	 552     download              backup.download
//	 568   logs                    server.logs
//
// THE FOUR GROUPING DECISIONS, with the why:
//
//  1. `connection` and `build` do NOT become operations of their own — they are
//     fields of `server.status`. Two routes fewer, and the screen fetches ONCE
//     instead of three times.
//
//  2. `update` does NOT become an operation of its own — it is a value of the
//     closed `Verbo` set in `server.action`. The code itself already documents
//     that updating IS restarting: steamcmd runs at container start, there is no
//     "update without restarting" path.
//
//  3. `groups` and `bans` do NOT become operations of their own — they are
//     sections of the config document, covered by `settings.get`/`settings.patch`
//     with an allowlist. A group and a ban are FIELDS, not resources.
//
//  4. `rawconfig` is NOT ported as it stands. The read becomes `settings.get`;
//     the write becomes `settings.patch` with ENUMERATED fields, on the model of
//     the `serverFields`/`groupFields` that already exist in
//     adapter_server_settings.go. The measured nuance, so that review neither
//     over- nor underestimates the risk: the path is NOT controlled by the
//     client (it comes from `adapter.ConfigPath`) and the content already goes
//     through unmarshal plus the `userGroups` requirement. Even so it is
//     **arbitrary content in a file the server executes as config**, protected
//     by a single semantic guard. Narrowing that is the whole point.
//
// WHAT STOPS BEING REACHABLE FROM OUTSIDE, and why:
//
//	BackupPath   returned a host path. Replaced by an opaque Handle +
//	             stream in backup.download — a path never crosses the boundary.
//	ConfigPath   same: the client does not choose the file, the adapter does.
//	RawConfig    (raw write) no direct replacement — only settings.patch with
//	             enumerated fields. It is a deliberate REDUCTION of capability.
//
// world.export / world.import / backup.download still exist, but what crosses
// the boundary is an opaque `Handle` plus a stream — never a path.

// OpName is the name of a catalog operation. Its own type, not a string, so
// that a loose literal key in the registry does not compile by accident.
type OpName string

// The 23 operations, in 7 families.
//
// The shape is always `familia.verbo`. There is NO free-execution operation, and
// there is no "other" — adding capability requires adding a constant HERE plus a
// registry entry, and the exhaustiveness test demands both sides.
const (
	// server — the process lifecycle and state.
	OpServerList   OpName = "server.list"   // the node's server inventory
	OpServerStatus OpName = "server.status" // state + connection + build, in a single fetch
	OpServerAction OpName = "server.action" // start|stop|restart|update, via Verbo
	OpServerLogs   OpName = "server.logs"   // tail of the container logs

	// world — worlds/saves. The family the acceptance criteria exercise.
	OpWorldList      OpName = "world.list"
	OpWorldSwitch    OpName = "world.switch"
	OpWorldExport    OpName = "world.export"
	OpWorldImport    OpName = "world.import"
	OpWorldRename    OpName = "world.rename"
	OpWorldDuplicate OpName = "world.duplicate"
	OpWorldDelete    OpName = "world.delete"

	// settings — the game's config document, with an allowlist of fields.
	OpSettingsGet   OpName = "settings.get"
	OpSettingsPatch OpName = "settings.patch"

	// runtime — the compose env vars (what only takes effect by recreating the container).
	OpRuntimeGet   OpName = "runtime.get"
	OpRuntimePatch OpName = "runtime.patch"

	// backup — world snapshots.
	OpBackupList     OpName = "backup.list"
	OpBackupCreate   OpName = "backup.create"
	OpBackupRestore  OpName = "backup.restore"
	OpBackupDownload OpName = "backup.download"

	// trainer — tl-agent. The panel only reads a snapshot and writes the desired
	// state; ALL the intelligence lives in tl-agent (see the header of trainer.go).
	OpTrainerStatus  OpName = "trainer.status"
	OpTrainerApply   OpName = "trainer.apply"
	OpTrainerDesired OpName = "trainer.desired"

	// history — the server's telemetry samples.
	OpHistoryList OpName = "history.list"
)

// FamiliasValidas is the CLOSED set of families. An operation whose family is
// not here fails TestCatalogoNomesBemFormados.
var FamiliasValidas = map[string]bool{
	"server":   true,
	"world":    true,
	"settings": true,
	"runtime":  true,
	"backup":   true,
	"trainer":  true,
	"history":  true,
}

// TodasAsOps is the canonical list.
//
// ⚠️ THE COUNT IS NOT A CONTRACT. No test asserts `len(TodasAsOps) == 23`, and
// that is deliberate: an absolute count is itself the defect — an invariants
// script once stayed stuck at 21 when there were already 65, and nobody noticed
// because the number looked intentional. What gets asserted is a PROPERTY (every
// name distinct, shape `familia.verbo`, family in the closed set, constant and
// registry entry in correspondence), never a number.
var TodasAsOps = []OpName{
	OpServerList, OpServerStatus, OpServerAction, OpServerLogs,
	OpWorldList, OpWorldSwitch, OpWorldExport, OpWorldImport,
	OpWorldRename, OpWorldDuplicate, OpWorldDelete,
	OpSettingsGet, OpSettingsPatch,
	OpRuntimeGet, OpRuntimePatch,
	OpBackupList, OpBackupCreate, OpBackupRestore, OpBackupDownload,
	OpTrainerStatus, OpTrainerApply, OpTrainerDesired,
	OpHistoryList,
}
