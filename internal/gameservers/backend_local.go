package gameservers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
)

// BackendLocal is the Backend that runs WHERE THE DISK IS: inside the lab-agent,
// on the node. It is today's `os.*` code, reached by operation name instead of by
// an HTTP route of the panel.
//
// ─────────────────────────────────────────────────────────────────────────────
// WHY THE ROUND-TRIP CRITERION POINTS AT THE ENSHROUDED JSON
//
// Decided by the operator and recorded in the planning notes.
//
// The criterion used to say ".ini rewritten preserving owner and mode". Measured
// in this fork (the fork where the lab-agent is born): `internal/gameservers`
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
// ─────────────────────────────────────────────────────────────────────────────
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
	cofre *cofreHandles
}

// NovoBackendLocal wraps an already constructed Manager.
func NovoBackendLocal(m *Manager, no string) *BackendLocal {
	return &BackendLocal{m: m, no: no, cofre: novoCofre(vidaPadraoHandle)}
}

func (b *BackendLocal) Descrever() string { return "local:" + b.no }

func (b *BackendLocal) Abrir(_ context.Context, h Handle) (io.ReadCloser, error) {
	return b.cofre.Abrir(h)
}

// recebidoMax caps what a client can push to the node at once.
// 600 MiB is the same ceiling the old import handler already applied — it covers
// a large world with room to spare and leaves no room for an infinite upload.
const recebidoMax = 600 << 20

// Receber writes the bytes into a temporary on the node and returns the Handle.
//
// The file is EPHEMERAL and ANONYMOUS: the client chose neither the name nor the
// directory, and it disappears once the handle is consumed or expires. That is
// what lets `world.import` exist without the panel knowing the node's disk.
func (b *BackendLocal) Receber(_ context.Context, r io.Reader) (Handle, error) {
	f, err := os.CreateTemp("", "recebido-*.bin")
	if err != nil {
		return "", err
	}
	n, err := io.Copy(f, io.LimitReader(r, recebidoMax+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	if n > recebidoMax {
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("upload above the %d byte limit", recebidoMax)
	}
	// Scope: the received handle is valid for ANY server on this node, because
	// whoever sends it has not yet said which one they will import into. The real
	// restriction is at consumption — world.import resolves the server, then uses the path.
	h, err := b.cofre.Cunhar(escopoRecebido, f.Name(), true)
	if err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return h, nil
}

// escopoRecebido is the scope of the artifacts that CAME IN to the node. Its own
// constant so that an upload handle never collides with a real server's scope.
const escopoRecebido = "\x00recebido"

// ─────────────────────────────────────────────────────────────────────────────
// ENVELOPES
//
// The operations' input documents. They live here because this is where they are
// decoded; the HTTP back-end does not know them (it forwards bytes), and it is
// precisely by not knowing them that it cannot diverge from them.
//
// Convention: every envelope that speaks of a server carries `servidor` with the
// inventory ID. NEVER a path — that is the whole boundary.

type reqServidor struct {
	Servidor string `json:"servidor"`
}

type reqAcao struct {
	Servidor string `json:"servidor"`
	Verbo    Verbo  `json:"verbo"`
}

type reqLogs struct {
	Servidor string `json:"servidor"`
	Tail     string `json:"tail"`
}

type reqMundo struct {
	Servidor string `json:"servidor"`
	Mundo    string `json:"mundo"`
}

type reqRenomear struct {
	Servidor string `json:"servidor"`
	De       string `json:"de"`
	Para     string `json:"para"`
}

type reqImportar struct {
	Servidor string `json:"servidor"`
	Nome     string `json:"nome"`
	// Handle of a zip that is ALREADY on the node (typically coming from
	// world.export). Sending the zip from the panel to the node is an upload, it
	// needs a route of its own with a limit and came later — it was declared as a
	// gap, not faked here with base64 inside a 1 MiB envelope.
	Handle Handle `json:"handle"`
}

// reqSettingsPatch covers the four sections that triage merged into settings.*:
// game config, server options, groups and bans.
//
// Grupos and Banidos are POINTERS so as to separate "do not touch this section"
// (nil) from "empty this section" (pointer to an empty list). With a plain slice
// the two cases are the same value, and wiping every ban by accident would be
// indistinguishable from not sending the section at all.
type reqSettingsPatch struct {
	Servidor  string                 `json:"servidor"`
	Jogo      map[string]interface{} `json:"jogo,omitempty"`
	Server    map[string]interface{} `json:"server,omitempty"`
	Grupo     map[string]interface{} `json:"grupo,omitempty"`
	Grupos    *[]Group               `json:"grupos,omitempty"`
	Banidos   *[]string              `json:"banidos,omitempty"`
	Reiniciar bool                   `json:"reiniciar,omitempty"`
}

type reqRuntimePatch struct {
	Servidor string            `json:"servidor"`
	Patch    map[string]string `json:"patch"`
}

type reqBackupArquivo struct {
	Servidor string `json:"servidor"`
	Arquivo  string `json:"arquivo"`
}

type reqHistorico struct {
	Servidor string `json:"servidor"`
	Horas    int    `json:"horas"`
}

// ─────────────────────────────────────────────────────────────────────────────
// DISPATCH
//
// A `switch` over the catalog. The `default` is a sentinel: TestBackendLocalServeTodasAsOps
// walks TodasAsOps and fails if any of them lands here. It is what prevents
// "constant declared, operation never implemented" — the same hole the third
// exhaustiveness test closes from the other side.

// naoImplementada is the default's sentinel. Fixed text so the test recognizes
// it without depending on formatting.
const naoImplementada = "operation not implemented in the local back end"

func (b *BackendLocal) Executar(ctx context.Context, op OpName, corpo json.RawMessage) (json.RawMessage, error) {
	if len(corpo) == 0 {
		corpo = json.RawMessage("{}")
	}
	switch op {

	// ── server ───────────────────────────────────────────────────────────────
	case OpServerList:
		return empacota(b.m.List())

	case OpServerStatus:
		s, err := b.servidor(corpo)
		if err != nil {
			return nil, err
		}
		// Triage merged `connection` and `build` in here: the screen fetches ONCE
		// instead of three times. No new computation — the three values are the
		// same ones the three old routes returned.
		return empacota(map[string]interface{}{
			"status":     b.m.Status(ctx, s),
			"connection": b.m.ConnectionInfo(s),
			"build":      b.m.Build(s),
		})

	case OpServerAction:
		var r reqAcao
		if err := json.Unmarshal(corpo, &r); err != nil {
			return nil, erroCorpo(err)
		}
		s, ok := b.m.Get(r.Servidor)
		if !ok {
			return nil, erroServidor(r.Servidor)
		}
		if !VerbosValidos[r.Verbo] {
			return nil, fmt.Errorf("invalid verb: %q", string(r.Verbo))
		}
		// `update` IS a restart with declared intent: the image runs steamcmd at
		// container start. UpdateNow is the path the panel already uses — kept,
		// not rewritten.
		if r.Verbo == VerboUpdate {
			if err := b.m.UpdateNow(ctx, s); err != nil {
				return nil, err
			}
			return empacota(map[string]interface{}{"ok": true, "verbo": string(r.Verbo)})
		}
		if err := b.m.Action(ctx, s, string(r.Verbo)); err != nil {
			return nil, err
		}
		return empacota(map[string]interface{}{"ok": true, "verbo": string(r.Verbo)})

	case OpServerLogs:
		var r reqLogs
		if err := json.Unmarshal(corpo, &r); err != nil {
			return nil, erroCorpo(err)
		}
		s, ok := b.m.Get(r.Servidor)
		if !ok {
			return nil, erroServidor(r.Servidor)
		}
		if r.Tail == "" {
			r.Tail = "200" // same default as the old handler
		}
		out, err := b.m.Logs(ctx, s, r.Tail)
		if err != nil {
			return nil, err
		}
		return empacota(map[string]interface{}{"logs": out})

	// ── world ────────────────────────────────────────────────────────────────
	case OpWorldList:
		s, err := b.servidor(corpo)
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
		return empacota(map[string]interface{}{"worlds": w})

	case OpWorldSwitch:
		var r reqMundo
		if err := json.Unmarshal(corpo, &r); err != nil {
			return nil, erroCorpo(err)
		}
		s, ok := b.m.Get(r.Servidor)
		if !ok {
			return nil, erroServidor(r.Servidor)
		}
		if err := b.m.SwitchWorld(s, r.Mundo); err != nil {
			return nil, err
		}
		return empacota(map[string]interface{}{"ok": true, "mundo": r.Mundo})

	case OpWorldExport:
		var r reqMundo
		if err := json.Unmarshal(corpo, &r); err != nil {
			return nil, erroCorpo(err)
		}
		s, ok := b.m.Get(r.Servidor)
		if !ok {
			return nil, erroServidor(r.Servidor)
		}
		zip, err := b.m.ExportWorld(s, r.Mundo)
		if err != nil {
			return nil, err
		}
		// HERE is where the path STOPS. The temporary zip never leaves this
		// process; what crosses is the token. `efemero: true` because the file is
		// ours and disappears when whoever downloaded it closes.
		h, err := b.cofre.Cunhar(s.ID, zip, true)
		if err != nil {
			return nil, err
		}
		return empacota(map[string]interface{}{"handle": string(h), "nome": r.Mundo + ".zip"})

	case OpWorldImport:
		var r reqImportar
		if err := json.Unmarshal(corpo, &r); err != nil {
			return nil, erroCorpo(err)
		}
		s, ok := b.m.Get(r.Servidor)
		if !ok {
			return nil, erroServidor(r.Servidor)
		}
		// The handle may have two legitimate owners: the server itself (a world
		// exported from it) or the upload scope (a zip the panel has just sent).
		// Any other scope is a refusal.
		a, err := b.cofre.resolver(r.Handle, s.ID)
		if err != nil {
			if a, err = b.cofre.resolver(r.Handle, escopoRecebido); err != nil {
				return nil, err
			}
		}
		if err := b.m.ImportWorld(s, r.Nome, a.caminho); err != nil {
			return nil, err
		}
		return empacota(map[string]interface{}{"ok": true, "nome": r.Nome})

	case OpWorldRename:
		var r reqRenomear
		if err := json.Unmarshal(corpo, &r); err != nil {
			return nil, erroCorpo(err)
		}
		s, ok := b.m.Get(r.Servidor)
		if !ok {
			return nil, erroServidor(r.Servidor)
		}
		if err := b.m.RenameWorld(s, r.De, r.Para); err != nil {
			return nil, err
		}
		return empacota(map[string]interface{}{"ok": true})

	case OpWorldDuplicate:
		var r reqRenomear
		if err := json.Unmarshal(corpo, &r); err != nil {
			return nil, erroCorpo(err)
		}
		s, ok := b.m.Get(r.Servidor)
		if !ok {
			return nil, erroServidor(r.Servidor)
		}
		if err := b.m.DuplicateWorld(s, r.De, r.Para); err != nil {
			return nil, err
		}
		return empacota(map[string]interface{}{"ok": true})

	case OpWorldDelete:
		var r reqMundo
		if err := json.Unmarshal(corpo, &r); err != nil {
			return nil, erroCorpo(err)
		}
		s, ok := b.m.Get(r.Servidor)
		if !ok {
			return nil, erroServidor(r.Servidor)
		}
		if err := b.m.DeleteWorld(s, r.Mundo); err != nil {
			return nil, err
		}
		return empacota(map[string]interface{}{"ok": true})

	// ── settings ─────────────────────────────────────────────────────────────
	case OpSettingsGet:
		s, err := b.servidor(corpo)
		if err != nil {
			return nil, err
		}
		return b.lerSettings(s)

	case OpSettingsPatch:
		var r reqSettingsPatch
		if err := json.Unmarshal(corpo, &r); err != nil {
			return nil, erroCorpo(err)
		}
		s, ok := b.m.Get(r.Servidor)
		if !ok {
			return nil, erroServidor(r.Servidor)
		}
		return b.gravaSettings(ctx, s, r)

	// ── runtime ──────────────────────────────────────────────────────────────
	case OpRuntimeGet:
		s, err := b.servidor(corpo)
		if err != nil {
			return nil, err
		}
		opts, err := b.m.Runtime(s)
		if err != nil {
			return nil, err
		}
		return empacota(map[string]interface{}{"options": opts})

	case OpRuntimePatch:
		var r reqRuntimePatch
		if err := json.Unmarshal(corpo, &r); err != nil {
			return nil, erroCorpo(err)
		}
		s, ok := b.m.Get(r.Servidor)
		if !ok {
			return nil, erroServidor(r.Servidor)
		}
		if err := b.m.SetRuntime(s, r.Patch); err != nil {
			return nil, err
		}
		return empacota(map[string]interface{}{"ok": true})

	// ── backup ───────────────────────────────────────────────────────────────
	case OpBackupList:
		s, err := b.servidor(corpo)
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
		return empacota(map[string]interface{}{"backups": list})

	case OpBackupCreate:
		s, err := b.servidor(corpo)
		if err != nil {
			return nil, err
		}
		// The stamp is generated ON THE NODE, as it already was in the handler.
		// Letting the client send the stamp would hand it the file name — halfway
		// to choosing where to write.
		nome, err := b.m.CreateBackup(s, carimboAgora())
		if err != nil {
			return nil, err
		}
		return empacota(map[string]interface{}{"ok": true, "arquivo": nome})

	case OpBackupRestore:
		var r reqBackupArquivo
		if err := json.Unmarshal(corpo, &r); err != nil {
			return nil, erroCorpo(err)
		}
		s, ok := b.m.Get(r.Servidor)
		if !ok {
			return nil, erroServidor(r.Servidor)
		}
		// The stop→restore→start sequence comes from the panel's handler, verbatim:
		// writing to the savegame with the server up corrupts the save. It migrates
		// here because it has to run WHERE THE CONTAINER IS — leaving it on the
		// panel's side would mean three network round trips in the middle of an
		// operation that must not be interrupted.
		if err := b.m.Action(ctx, s, string(VerboStop)); err != nil {
			return nil, fmt.Errorf("could not stop the server before restoring: %w", err)
		}
		erroRestore := b.m.RestoreBackup(s, r.Arquivo, carimboAgora())
		erroStart := b.m.Action(ctx, s, string(VerboStart))
		if erroRestore != nil {
			return nil, erroRestore
		}
		aviso := ""
		if erroStart != nil {
			aviso = "restored, but the server failed to start: " + erroStart.Error()
		}
		return empacota(map[string]interface{}{"ok": true, "aviso": aviso})

	case OpBackupDownload:
		var r reqBackupArquivo
		if err := json.Unmarshal(corpo, &r); err != nil {
			return nil, erroCorpo(err)
		}
		s, ok := b.m.Get(r.Servidor)
		if !ok {
			return nil, erroServidor(r.Servidor)
		}
		p, err := b.m.BackupPath(s, r.Arquivo)
		if err != nil {
			return nil, err
		}
		// `efemero: false` — the backup belongs to the user, it is not a temp of
		// ours. Deleting it when the download closes would destroy data.
		h, err := b.cofre.Cunhar(s.ID, p, false)
		if err != nil {
			return nil, err
		}
		return empacota(map[string]interface{}{"handle": string(h), "nome": r.Arquivo})

	// ── trainer ──────────────────────────────────────────────────────────────
	case OpTrainerStatus:
		if !b.m.TrainerAvailable() {
			return nil, ErroTrainerAusente
		}
		return b.m.TrainerSnapshot(ctx)

	case OpTrainerApply:
		if !b.m.TrainerAvailable() {
			return nil, ErroTrainerAusente
		}
		msg, err := b.m.TrainerApply(ctx)
		if err != nil {
			return nil, err
		}
		return empacota(map[string]interface{}{"ok": true, "message": msg})

	case OpTrainerDesired:
		if !b.m.TrainerAvailable() {
			return nil, ErroTrainerAusente
		}
		var d TrainerDesired
		if err := json.Unmarshal(corpo, &d); err != nil {
			return nil, erroCorpo(err)
		}
		return b.m.TrainerSetDesired(ctx, d)

	// ── history ──────────────────────────────────────────────────────────────
	case OpHistoryList:
		var r reqHistorico
		if err := json.Unmarshal(corpo, &r); err != nil {
			return nil, erroCorpo(err)
		}
		s, ok := b.m.Get(r.Servidor)
		if !ok {
			return nil, erroServidor(r.Servidor)
		}
		if r.Horas <= 0 {
			r.Horas = 6 // same default as the old handler
		}
		am := b.m.History(s.ID, r.Horas)
		if am == nil {
			am = []Sample{}
		}
		return empacota(map[string]interface{}{"samples": am})
	}

	return nil, fmt.Errorf("%s: %s", naoImplementada, string(op))
}

// ── helpers ──────────────────────────────────────────────────────────────────

// servidor decodes the minimal envelope and resolves the Server.
func (b *BackendLocal) servidor(corpo json.RawMessage) (Server, error) {
	var r reqServidor
	if err := json.Unmarshal(corpo, &r); err != nil {
		return Server{}, erroCorpo(err)
	}
	s, ok := b.m.Get(r.Servidor)
	if !ok {
		return Server{}, erroServidor(r.Servidor)
	}
	return s, nil
}

func (b *BackendLocal) lerSettings(s Server) (json.RawMessage, error) {
	jogo, err := b.m.Settings(s)
	if err != nil {
		return nil, err
	}
	srv, err := b.m.ServerSettings(s)
	if err != nil {
		return nil, err
	}
	grupos, err := b.m.Groups(s)
	if err != nil {
		return nil, err
	}
	if grupos == nil {
		grupos = []Group{}
	}
	banidos, err := b.m.Bans(s)
	if err != nil {
		return nil, err
	}
	if banidos == nil {
		banidos = []string{}
	}
	return empacota(map[string]interface{}{
		"jogo": jogo, "server": srv, "grupos": grupos, "banidos": banidos,
	})
}

// gravaSettings applies only the sections present in the envelope.
//
// No raw writing: `rawconfig` was NOT ported. Each section goes through the
// allowlist that already exists in the adapters — it is the narrowing starting
// to take effect, not a new guard invented here.
func (b *BackendLocal) gravaSettings(ctx context.Context, s Server, r reqSettingsPatch) (json.RawMessage, error) {
	if len(r.Jogo) > 0 {
		if err := b.m.SaveSettings(s, r.Jogo); err != nil {
			return nil, err
		}
	}
	if len(r.Server) > 0 || len(r.Grupo) > 0 {
		if err := b.m.SaveServerSettings(s, r.Server, r.Grupo); err != nil {
			return nil, err
		}
	}
	if r.Grupos != nil {
		if err := b.m.SaveGroups(s, *r.Grupos); err != nil {
			return nil, err
		}
	}
	if r.Banidos != nil {
		if err := b.m.SaveBans(s, *r.Banidos); err != nil {
			return nil, err
		}
	}
	reiniciado := false
	if r.Reiniciar {
		// Same as the old handler: a restart failure neither undoes the write nor
		// becomes an error of the operation — the config is ALREADY on disk, and
		// saying it failed would make the operator write it all over again.
		if err := b.m.Action(ctx, s, string(VerboRestart)); err == nil {
			reiniciado = true
		}
	}
	return empacota(map[string]interface{}{"ok": true, "reiniciado": reiniciado})
}

// carimboAgora is the same format the panel uses today (stampNow).
func carimboAgora() string { return time.Now().Format("20060102-150405") }

func erroCorpo(err error) error { return fmt.Errorf("invalid body: %w", err) }

func erroServidor(id string) error { return fmt.Errorf("server '%s' not found", id) }

// empacota serializes the response. A single function so that no operation
// invents an output format of its own.
func empacota(v interface{}) (json.RawMessage, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(b), nil
}
