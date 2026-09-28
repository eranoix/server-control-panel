package gameservers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
)

type BackendLocal struct {
	m     *Manager
	no    string
	vault *handleVault
}

func NewBackendLocal(m *Manager, no string) *BackendLocal {
	return &BackendLocal{m: m, no: no, vault: newHandleVault(defaultHandleTTL)}
}

func (b *BackendLocal) Describe() string { return "local:" + b.no }

func (b *BackendLocal) Open(_ context.Context, h Handle) (io.ReadCloser, error) {
	return b.vault.Open(h)
}

const maxReceived = 600 << 20

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
	h, err := b.vault.Mint(receivedScope, f.Name(), true)
	if err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return h, nil
}

const receivedScope = "\x00received"

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
	Handle   Handle `json:"handle"`
}

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
			r.Tail = "200"
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
			w = []World{}
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
			r.Hours = 6
		}
		am := b.m.History(s.ID, r.Hours)
		if am == nil {
			am = []Sample{}
		}
		return pack(map[string]interface{}{"samples": am})
	}

	return nil, fmt.Errorf("%s: %s", notImplemented, string(op))
}

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
		if err := b.m.Action(ctx, s, string(VerbRestart)); err == nil {
			restarted = true
		}
	}
	return pack(map[string]interface{}{"ok": true, "restarted": restarted})
}

func nowStamp() string { return time.Now().Format("20060102-150405") }

func bodyError(err error) error { return fmt.Errorf("invalid body: %w", err) }

func serverNotFound(id string) error { return fmt.Errorf("server '%s' not found", id) }

func pack(v interface{}) (json.RawMessage, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(b), nil
}
