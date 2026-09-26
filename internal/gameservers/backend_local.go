package gameservers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
)

// BackendLocal is the Backend that runs WHERE THE DISK IS: inside the node-agent,
// on the node. It is today's `os.*` code, reached by operation name instead of by
// an HTTP route of the panel.
//
// # WHY THE ROUND-TRIP CRITERION POINTS AT THE ENSHROUDED JSON
//
// Decided by the operator and recorded in the planning notes.
//
// The criterion used to say ".ini rewritten preserving owner and mode". Measured
// in this fork (the fork where the node-agent is born): `internal/gameservers`
// has 10 files, the adapter map has only `enshrouded`, and grep for `.ini`
// returns ZERO. `PalWorldSettings.ini` lives in the OTHER fork.
//
// The property the criterion measures — owner and mode preserved, checked with
// `stat` — is FORMAT-AGNOSTIC. And the defect it measures is alive here: three
// writers of Enshrouded JSON, with `/opt/enshrouded/.active` as root:root in the
// middle of a 4711:4711 tree (photographed in production, closed since). Porting
// Palworld would be ~1,900 lines COPIED between forks — the third fork the merge
// would then have to fuse, which is the reason that option was rejected. A
// synthetic `.ini` would prove something against a file no screen uses.
//
// That is why the round-trip points here. Palworld comes in with the merge.
//
// # SCOPE: WHAT IS NOT TOUCHED HERE
//
// `StartSampler` and `History` stay in the Manager. Which side samples the
// history — agent or panel — changes the PLACE of the history file, which makes
// it DATA MIGRATION, and data migration belongs to the merge. Here `history.list`
// is only a read of what already exists.
//
// # NO NEW GLOBAL STATE
//
// There is no package-level `var` in this file, and that is a requirement
// verified by grep: the Manager arrives as a parameter and the handle vault is a
// field of the instance. Two BackendLocal in the same process share nothing —
// which is what makes it possible one day to serve two nodes from a single
// binary without either seeing the other's handles.
type BackendLocal struct {
	m     *Manager
	no    string
	vault *handleVault
}

// NewBackendLocal wraps an already constructed Manager.
func NewBackendLocal(m *Manager, no string) *BackendLocal {
	return &BackendLocal{m: m, no: no, vault: newHandleVault(defaultHandleTTL)}
}

func (b *BackendLocal) Describe() string { return "local:" + b.no }

func (b *BackendLocal) Open(_ context.Context, h Handle) (io.ReadCloser, error) {
	return b.vault.Open(h)
}

// maxReceived caps what a client can push to the node at once.
// 600 MiB is the same ceiling the old import handler already applied — it covers
// a large world with room to spare and leaves no room for an infinite upload.
const maxReceived = 600 << 20

// Receive writes the bytes into a temporary on the node and returns the Handle.
//
// The file is EPHEMERAL and ANONYMOUS: the client chose neither the name nor the
// directory, and it disappears once the handle is consumed or expires. That is
// what lets `world.import` exist without the panel knowing the node's disk.
func (b *BackendLocal) Receive(_ context.Context, r io.Reader) (Handle, error) {
	f, err := os.CreateTemp("", "received-*.bin")
	if err != nil {
		return "", err
	}
	n, err := io.Copy(f, io.LimitReader(r, maxReceived+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	if n > maxReceived {
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("upload above the %d byte limit", maxReceived)
	}
	// Scope: the received handle is valid for ANY server on this node, because
	// whoever sends it has not yet said which one they will import into. The real
	// restriction is at consumption — world.import resolves the server, then uses the path.
	h, err := b.vault.Mint(receivedScope, f.Name(), true)
	if err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return h, nil
}

// receivedScope is the scope of the artifacts that CAME IN to the node. Its own
// constant so that an upload handle never collides with a real server's scope.
const receivedScope = "\x00received"

// ENVELOPES
//
// The operations' input documents. They live here because this is where they are
// decoded; the HTTP back-end does not know them (it forwards bytes), and it is
// precisely by not knowing them that it cannot diverge from them.
//
// Convention: every envelope that speaks of a server carries `server_id` (wire
// key) with the inventory ID. NEVER a path: that is the whole boundary.

type reqServer struct {
	ServerID string `json:"server_id"`
}

type reqAction struct {
	ServerID string `json:"server_id"`
	Verb     Verb   `json:"verb"`
}

type reqLogs struct {
	ServerID string `json:"server_id"`
	Tail     string `json:"tail"`
}

type reqWorld struct {
	ServerID string `json:"server_id"`
	World    string `json:"world"`
}

type reqRename struct {
	ServerID string `json:"server_id"`
	From     string `json:"from"`
	To       string `json:"to"`
}

type reqImport struct {
	ServerID string `json:"server_id"`
	Name     string `json:"name"`
	// Handle of a zip that is ALREADY on the node (typically coming from
	// world.export). Sending the zip from the panel to the node is an upload, it
	// needs a route of its own with a limit and came later — it was declared as a
	// gap, not faked here with base64 inside a 1 MiB envelope.
	Handle Handle `json:"handle"`
}

// reqSettingsPatch covers the four sections that triage merged into settings.*:
// game config, server options, groups and bans.
//
// Groups and Banned are POINTERS so as to separate "do not touch this section"
// (nil) from "empty this section" (pointer to an empty list). With a plain slice
// the two cases are the same value, and wiping every ban by accident would be
// indistinguishable from not sending the section at all.
type reqSettingsPatch struct {
	ServerID string                 `json:"server_id"`
	Game     map[string]interface{} `json:"game,omitempty"`
	Server   map[string]interface{} `json:"server,omitempty"`
	Group    map[string]interface{} `json:"group,omitempty"`
	Groups   *[]Group               `json:"groups,omitempty"`
	Banned   *[]string              `json:"banned,omitempty"`
	Restart  bool                   `json:"restart,omitempty"`
}

type reqRuntimePatch struct {
	ServerID string            `json:"server_id"`
	Patch    map[string]string `json:"patch"`
}

type reqBackupFile struct {
	ServerID string `json:"server_id"`
	File     string `json:"file"`
}

type reqHistory struct {
	ServerID string `json:"server_id"`
	Hours    int    `json:"hours"`
}

// DISPATCH
//
// A `switch` over the catalog. The `default` is a sentinel: TestBackendLocalServesAllOps
// walks AllOps and fails if any of them lands here. It is what prevents
// "constant declared, operation never implemented" — the same hole the third
// exhaustiveness test closes from the other side.

// notImplemented is the default's sentinel. Fixed text so the test recognizes
// it without depending on formatting.
const notImplemented = "operation not implemented in the local back end"

func (b *BackendLocal) Execute(ctx context.Context, op OpName, body json.RawMessage) (json.RawMessage, error) {
	if len(body) == 0 {
		body = json.RawMessage("{}")
	}
	switch op {

	case OpServerList:
		return pack(b.m.List())

	case OpServerStatus:
		s, err := b.server(body)
		if err != nil {
			return nil, err
		}
		// Triage merged `connection` and `build` in here: the screen fetches ONCE
		// instead of three times. No new computation — the three values are the
		// same ones the three old routes returned.
		return pack(map[string]interface{}{
			"status":     b.m.Status(ctx, s),
			"connection": b.m.ConnectionInfo(s),
			"build":      b.m.Build(s),
		})

	case OpServerAction:
		var r reqAction
		if err := json.Unmarshal(body, &r); err != nil {
			return nil, bodyError(err)
		}
		s, ok := b.m.Get(r.ServerID)
		if !ok {
			return nil, serverNotFound(r.ServerID)
		}
		if !ValidVerbs[r.Verb] {
			return nil, fmt.Errorf("invalid verb: %q", string(r.Verb))
		}
		// `update` IS a restart with declared intent: the image runs steamcmd at
		// container start. UpdateNow is the path the panel already uses — kept,
		// not rewritten.
		if r.Verb == VerbUpdate {
			if err := b.m.UpdateNow(ctx, s); err != nil {
				return nil, err
			}
			return pack(map[string]interface{}{"ok": true, "verb": string(r.Verb)})
		}
		if err := b.m.Action(ctx, s, string(r.Verb)); err != nil {
			return nil, err
		}
		return pack(map[string]interface{}{"ok": true, "verb": string(r.Verb)})

	case OpServerLogs:
		var r reqLogs
		if err := json.Unmarshal(body, &r); err != nil {
			return nil, bodyError(err)
		}
		s, ok := b.m.Get(r.ServerID)
		if !ok {
			return nil, serverNotFound(r.ServerID)
		}
		if r.Tail == "" {
			r.Tail = "200" // same default as the old handler
		}
		out, err := b.m.Logs(ctx, s, r.Tail)
		if err != nil {
			return nil, err
		}
		return pack(map[string]interface{}{"logs": out})

	case OpWorldList:
		s, err := b.server(body)
		if err != nil {
			return nil, err
		}
		w, err := b.m.Worlds(s)
		if err != nil {
			return nil, err
		}
		if w == nil {
			w = []World{} // the old handler already normalized; `null` breaks the screen
		}
		return pack(map[string]interface{}{"worlds": w})

	case OpWorldSwitch:
		var r reqWorld
		if err := json.Unmarshal(body, &r); err != nil {
			return nil, bodyError(err)
		}
		s, ok := b.m.Get(r.ServerID)
		if !ok {
			return nil, serverNotFound(r.ServerID)
		}
		if err := b.m.SwitchWorld(s, r.World); err != nil {
			return nil, err
		}
		return pack(map[string]interface{}{"ok": true, "world": r.World})

	case OpWorldExport:
		var r reqWorld
		if err := json.Unmarshal(body, &r); err != nil {
			return nil, bodyError(err)
		}
		s, ok := b.m.Get(r.ServerID)
		if !ok {
			return nil, serverNotFound(r.ServerID)
		}
		zip, err := b.m.ExportWorld(s, r.World)
		if err != nil {
			return nil, err
		}
		// HERE is where the path STOPS. The temporary zip never leaves this
		// process; what crosses is the token. `ephemeral: true` because the file is
		// ours and disappears when whoever downloaded it closes.
		h, err := b.vault.Mint(s.ID, zip, true)
		if err != nil {
			return nil, err
		}
		return pack(map[string]interface{}{"handle": string(h), "name": r.World + ".zip"})

	case OpWorldImport:
		var r reqImport
		if err := json.Unmarshal(body, &r); err != nil {
			return nil, bodyError(err)
		}
		s, ok := b.m.Get(r.ServerID)
		if !ok {
			return nil, serverNotFound(r.ServerID)
		}
		// The handle may have two legitimate owners: the server itself (a world
		// exported from it) or the upload scope (a zip the panel has just sent).
		// Any other scope is a refusal.
		a, err := b.vault.resolver(r.Handle, s.ID)
		if err != nil {
			if a, err = b.vault.resolver(r.Handle, receivedScope); err != nil {
				return nil, err
			}
		}
		if err := b.m.ImportWorld(s, r.Name, a.path); err != nil {
			return nil, err
		}
		return pack(map[string]interface{}{"ok": true, "name": r.Name})

	case OpWorldRename:
		var r reqRename
		if err := json.Unmarshal(body, &r); err != nil {
			return nil, bodyError(err)
		}
		s, ok := b.m.Get(r.ServerID)
		if !ok {
			return nil, serverNotFound(r.ServerID)
		}
		if err := b.m.RenameWorld(s, r.From, r.To); err != nil {
			return nil, err
		}
		return pack(map[string]interface{}{"ok": true})

	case OpWorldDuplicate:
		var r reqRename
		if err := json.Unmarshal(body, &r); err != nil {
			return nil, bodyError(err)
		}
		s, ok := b.m.Get(r.ServerID)
		if !ok {
			return nil, serverNotFound(r.ServerID)
		}
		if err := b.m.DuplicateWorld(s, r.From, r.To); err != nil {
			return nil, err
		}
		return pack(map[string]interface{}{"ok": true})

	case OpWorldDelete:
		var r reqWorld
		if err := json.Unmarshal(body, &r); err != nil {
			return nil, bodyError(err)
		}
		s, ok := b.m.Get(r.ServerID)
		if !ok {
			return nil, serverNotFound(r.ServerID)
		}
		if err := b.m.DeleteWorld(s, r.World); err != nil {
			return nil, err
		}
		return pack(map[string]interface{}{"ok": true})

	case OpSettingsGet:
		s, err := b.server(body)
		if err != nil {
			return nil, err
		}
		return b.readSettings(s)

	case OpSettingsPatch:
		var r reqSettingsPatch
		if err := json.Unmarshal(body, &r); err != nil {
			return nil, bodyError(err)
		}
		s, ok := b.m.Get(r.ServerID)
		if !ok {
			return nil, serverNotFound(r.ServerID)
		}
		return b.writeSettings(ctx, s, r)

	case OpRuntimeGet:
		s, err := b.server(body)
		if err != nil {
			return nil, err
		}
		opts, err := b.m.Runtime(s)
		if err != nil {
			return nil, err
		}
		return pack(map[string]interface{}{"options": opts})

	case OpRuntimePatch:
		var r reqRuntimePatch
		if err := json.Unmarshal(body, &r); err != nil {
			return nil, bodyError(err)
		}
		s, ok := b.m.Get(r.ServerID)
		if !ok {
			return nil, serverNotFound(r.ServerID)
		}
		if err := b.m.SetRuntime(s, r.Patch); err != nil {
			return nil, err
		}
		return pack(map[string]interface{}{"ok": true})

	case OpBackupList:
		s, err := b.server(body)
		if err != nil {
			return nil, err
		}
		list, err := b.m.Backups(s)
		if err != nil {
			return nil, err
		}
		if list == nil {
			list = []Backup{}
		}
		return pack(map[string]interface{}{"backups": list})

	case OpBackupCreate:
		s, err := b.server(body)
		if err != nil {
			return nil, err
		}
		// The stamp is generated ON THE NODE, as it already was in the handler.
		// Letting the client send the stamp would hand it the file name — halfway
		// to choosing where to write.
		name, err := b.m.CreateBackup(s, nowStamp())
		if err != nil {
			return nil, err
		}
		return pack(map[string]interface{}{"ok": true, "file": name})

	case OpBackupRestore:
		var r reqBackupFile
		if err := json.Unmarshal(body, &r); err != nil {
			return nil, bodyError(err)
		}
		s, ok := b.m.Get(r.ServerID)
		if !ok {
			return nil, serverNotFound(r.ServerID)
		}
		// The stop→restore→start sequence comes from the panel's handler, verbatim:
		// writing to the savegame with the server up corrupts the save. It migrates
		// here because it has to run WHERE THE CONTAINER IS — leaving it on the
		// panel's side would mean three network round trips in the middle of an
		// operation that must not be interrupted.
		if err := b.m.Action(ctx, s, string(VerbStop)); err != nil {
			return nil, fmt.Errorf("could not stop the server before restoring: %w", err)
		}
		restoreErr := b.m.RestoreBackup(s, r.File, nowStamp())
		startErr := b.m.Action(ctx, s, string(VerbStart))
		if restoreErr != nil {
			return nil, restoreErr
		}
		warning := ""
		if startErr != nil {
			warning = "restored, but the server failed to start: " + startErr.Error()
		}
		return pack(map[string]interface{}{"ok": true, "warning": warning})

	case OpBackupDownload:
		var r reqBackupFile
		if err := json.Unmarshal(body, &r); err != nil {
			return nil, bodyError(err)
		}
		s, ok := b.m.Get(r.ServerID)
		if !ok {
			return nil, serverNotFound(r.ServerID)
		}
		p, err := b.m.BackupPath(s, r.File)
		if err != nil {
			return nil, err
		}
		// `ephemeral: false`: the backup belongs to the user, it is not a temp of
		// ours. Deleting it when the download closes would destroy data.
		h, err := b.vault.Mint(s.ID, p, false)
		if err != nil {
			return nil, err
		}
		return pack(map[string]interface{}{"handle": string(h), "name": r.File})

	case OpTrainerStatus:
		if !b.m.TrainerAvailable() {
			return nil, ErrTrainerMissing
		}
		return b.m.TrainerSnapshot(ctx)

	case OpTrainerApply:
		if !b.m.TrainerAvailable() {
			return nil, ErrTrainerMissing
		}
		msg, err := b.m.TrainerApply(ctx)
		if err != nil {
			return nil, err
		}
		return pack(map[string]interface{}{"ok": true, "message": msg})

	case OpTrainerDesired:
		if !b.m.TrainerAvailable() {
			return nil, ErrTrainerMissing
		}
		var d TrainerDesired
		if err := json.Unmarshal(body, &d); err != nil {
			return nil, bodyError(err)
		}
		return b.m.TrainerSetDesired(ctx, d)

	case OpHistoryList:
		var r reqHistory
		if err := json.Unmarshal(body, &r); err != nil {
			return nil, bodyError(err)
		}
		s, ok := b.m.Get(r.ServerID)
		if !ok {
			return nil, serverNotFound(r.ServerID)
		}
		if r.Hours <= 0 {
			r.Hours = 6 // same default as the old handler
		}
		am := b.m.History(s.ID, r.Hours)
		if am == nil {
			am = []Sample{}
		}
		return pack(map[string]interface{}{"samples": am})
	}

	return nil, fmt.Errorf("%s: %s", notImplemented, string(op))
}

// server decodes the minimal envelope and resolves the Server.
func (b *BackendLocal) server(body json.RawMessage) (Server, error) {
	var r reqServer
	if err := json.Unmarshal(body, &r); err != nil {
		return Server{}, bodyError(err)
	}
	s, ok := b.m.Get(r.ServerID)
	if !ok {
		return Server{}, serverNotFound(r.ServerID)
	}
	return s, nil
}

func (b *BackendLocal) readSettings(s Server) (json.RawMessage, error) {
	game, err := b.m.Settings(s)
	if err != nil {
		return nil, err
	}
	srv, err := b.m.ServerSettings(s)
	if err != nil {
		return nil, err
	}
	groups, err := b.m.Groups(s)
	if err != nil {
		return nil, err
	}
	if groups == nil {
		groups = []Group{}
	}
	banned, err := b.m.Bans(s)
	if err != nil {
		return nil, err
	}
	if banned == nil {
		banned = []string{}
	}
	return pack(map[string]interface{}{
		"game": game, "server": srv, "groups": groups, "banned": banned,
	})
}

// writeSettings applies only the sections present in the envelope.
//
// No raw writing: `rawconfig` was NOT ported. Each section goes through the
// allowlist that already exists in the adapters — it is the narrowing starting
// to take effect, not a new guard invented here.
func (b *BackendLocal) writeSettings(ctx context.Context, s Server, r reqSettingsPatch) (json.RawMessage, error) {
	if len(r.Game) > 0 {
		if err := b.m.SaveSettings(s, r.Game); err != nil {
			return nil, err
		}
	}
	if len(r.Server) > 0 || len(r.Group) > 0 {
		if err := b.m.SaveServerSettings(s, r.Server, r.Group); err != nil {
			return nil, err
		}
	}
	if r.Groups != nil {
		if err := b.m.SaveGroups(s, *r.Groups); err != nil {
			return nil, err
		}
	}
	if r.Banned != nil {
		if err := b.m.SaveBans(s, *r.Banned); err != nil {
			return nil, err
		}
	}
	restarted := false
	if r.Restart {
		// Same as the old handler: a restart failure neither undoes the write nor
		// becomes an error of the operation — the config is ALREADY on disk, and
		// saying it failed would make the operator write it all over again.
		if err := b.m.Action(ctx, s, string(VerbRestart)); err == nil {
			restarted = true
		}
	}
	return pack(map[string]interface{}{"ok": true, "restarted": restarted})
}

// nowStamp is the same format the panel uses today (stampNow).
func nowStamp() string { return time.Now().Format("20060102-150405") }

func bodyError(err error) error { return fmt.Errorf("invalid body: %w", err) }

func serverNotFound(id string) error { return fmt.Errorf("server '%s' not found", id) }

// pack serializes the response. A single function so that no operation
// invents an output format of its own.
func pack(v interface{}) (json.RawMessage, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(b), nil
}
