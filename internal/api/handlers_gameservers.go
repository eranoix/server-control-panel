package api

// Handlers for the "Games" page — rewritten to talk to the
// `gameservers.Backend` of the node where each server lives, instead of to the
// local Manager.
//
// # WHAT CHANGED, AND WHY IT MATTERS
//
// Before, each `case` called the Manager, which touched THIS machine's disk.
// That worked because the dashboard and the game lived together. They stop
// living together: the destination is now chosen by `Node.transport`, in a
// single place (gamebackend.go), and the VPS's dashboard keeps working because
// the non-agent transport falls through to the local back-end — which is the
// same code as ever.
//
// # NO FILE SEMANTICS HERE
//
// This file knows nothing about paths, owners or modes. Where there used to be
// `BackupPath` and `ExportWorld` returning a host path, there is now an opaque
// `Handle` and a stream coming from the back-end. If any handler here ever needs
// to build a path, the extraction failed, and the place to fix it is the
// `Backend` — not here. There is a pin counting zero occurrences.
//
// Routing: a single /api/gameservers/ prefix with manual path parsing, in the
// same style as the other groups (it avoids clashing with the "/api/" catch-all).
//
// RBAC: reading is open to any authenticated session; EVERY mutation requires
// the primary account. The check stays at the same points — it lives in
// `executaOp`, with `escrita: true`, and was not reimplemented in the agent:
// session and permission belong to the dashboard and already existed.

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"server-control-panel/internal/gameservers"
	"server-control-panel/internal/httpmw"
	"server-control-panel/internal/httpx"
)

// maxGameWorldImportBytes is the upload ceiling for a world (.zip) to import
// — 600 MiB of real content (the business limit checked just below, in
// `cab.Size > 600<<20`) plus 1 MiB of slack for the multipart fields and
// boundaries around the file. httpmw.MaxBody applies 25 MiB by default on
// every route; without the RegisterLargeBody in init() below, a real world
// (almost always well over 25 MiB) never came close to the 600 MB ceiling this
// handler promises — the global MaxBytesReader cut the body off before
// ParseMultipartForm finished reading, and the `cab.Size` check was never
// reached.
const maxGameWorldImportBytes = 600<<20 + 1<<20

func init() {
	httpmw.RegisterLargeBody(isGameWorldImportUpload, maxGameWorldImportBytes)
}

// isGameWorldImportUpload matches exactly POST /api/gameservers/<id>/worlds/import
// — the only route in the gameservers family that receives a large file (the
// rest are small JSON or downloads, with no request body).
func isGameWorldImportUpload(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/gameservers/")
	if rest == r.URL.Path {
		return false
	}
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	return len(parts) == 3 && parts[1] == "worlds" && parts[2] == "import"
}

// handleGameServers serves GET /api/gameservers (list + status of all of them).
//
// It walks the servers resolving EACH ONE's back-end: servers from different
// nodes coexist on the same screen. An error on one does not take the others
// down — it becomes that item's `err` field, exactly as `Statuses` already did.
// A page that disappears entirely because one node went down is worse than a
// page with one red line.
func (r *Router) handleGameServers(w http.ResponseWriter, req *http.Request) {
	if r.gameMgr == nil {
		httpx.WriteErr(w, http.StatusServiceUnavailable, "game manager unavailable")
		return
	}
	if req.Method != http.MethodGet {
		httpx.WriteErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	saida := []map[string]interface{}{}
	for _, s := range r.gameMgr.List() {
		item := map[string]interface{}{"id": s.ID, "name": s.Name, "game": s.Game, "node": s.No}

		back, destino, err := r.backendPara(s)
		if err != nil {
			item["err"] = err.Error()
			saida = append(saida, item)
			continue
		}
		env, _ := json.Marshal(map[string]string{"servidor": s.ID})
		doc, err := back.Executar(req.Context(), gameservers.OpServerStatus, env)
		if err != nil {
			_, msg := traduzErroDeNo(err, destino.Nome)
			item["err"] = msg
			saida = append(saida, item)
			continue
		}
		var completo map[string]interface{}
		if err := json.Unmarshal(doc, &completo); err != nil {
			item["err"] = "unreadable response from node '" + destino.Nome + "'"
			saida = append(saida, item)
			continue
		}
		for k, v := range completo {
			item[k] = v
		}
		saida = append(saida, item)
	}
	httpx.WriteJSON(w, map[string]interface{}{"servers": saida})
}

// handleGameServerSub serves /api/gameservers/<id>/<resource>[/<action>].
func (r *Router) handleGameServerSub(w http.ResponseWriter, req *http.Request) {
	if r.gameMgr == nil {
		httpx.WriteErr(w, http.StatusServiceUnavailable, "game manager unavailable")
		return
	}
	rest := strings.TrimPrefix(req.URL.Path, "/api/gameservers/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) < 2 || parts[0] == "" {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid route")
		return
	}
	id, resource := parts[0], parts[1]
	action := ""
	if len(parts) >= 3 {
		action = parts[2]
	}

	// The inventory is still the source for the SERVER (id, name, node). What
	// changed is that it is no longer the source for its STATE — that comes from
	// the node.
	srv, ok := r.gameMgr.Get(id)
	if !ok {
		httpx.WriteErr(w, http.StatusNotFound, "server '"+id+"' not found")
		return
	}

	switch resource {

	// ── life cycle ───────────────────────────────────────────────────────────

	case "action": // 27-case: `action`
		var body struct {
			Action string `json:"action"`
		}
		if err := httpx.DecodeBody(req.Body, &body); err != nil {
			httpx.WriteErr(w, http.StatusBadRequest, "invalid body")
			return
		}
		verbo := gameservers.Verbo(body.Action)
		if !gameservers.VerbosValidos[verbo] {
			httpx.WriteErr(w, http.StatusBadRequest, "invalid action")
			return
		}
		doc, ok := r.executaOp(w, req, srv, gameservers.OpServerAction,
			map[string]interface{}{"servidor": srv.ID, "verbo": string(verbo)}, true)
		if !ok {
			return
		}
		escreveBruto(w, doc)

	case "update": // 27-case: `update` — virou VERBO de server.action
		// Updating IS restarting (steamcmd runs when the container starts). The
		// route still exists because the screen still calls it; what changed is
		// that it is no longer an operation of the catalogue's own.
		doc, ok := r.executaOp(w, req, srv, gameservers.OpServerAction,
			map[string]interface{}{"servidor": srv.ID, "verbo": string(gameservers.VerboUpdate)}, true)
		if !ok {
			return
		}
		escreveBruto(w, doc)

	case "logs": // 27-case: `logs`
		tail := req.URL.Query().Get("tail")
		if tail == "" {
			tail = "200"
		}
		doc, ok := r.executaOp(w, req, srv, gameservers.OpServerLogs,
			map[string]interface{}{"servidor": srv.ID, "tail": tail}, false)
		if !ok {
			return
		}
		escreveBruto(w, doc)

	// ── fields of server.status (no longer routes of their own) ──────────────

	case "connection", "build": // 27-case: `connection` e `build`
		// The triage merged the two into `server.status`. The routes still answer
		// so as not to break the old screen during the transition, but they serve
		// the document's SECTION — not a lookup of their own.
		doc, ok := r.executaOp(w, req, srv, gameservers.OpServerStatus,
			map[string]interface{}{"servidor": srv.ID}, false)
		if !ok {
			return
		}
		var completo map[string]json.RawMessage
		if err := json.Unmarshal(doc, &completo); err != nil {
			httpx.WriteErr(w, http.StatusBadGateway, "unreadable response from the node")
			return
		}
		escreveBruto(w, completo[resource])

	// ── settings sections (no longer routes of their own) ────────────────────

	case "groups": // 27-case: `groups`
		if req.Method == http.MethodGet {
			doc, ok := r.executaOp(w, req, srv, gameservers.OpSettingsGet,
				map[string]interface{}{"servidor": srv.ID}, false)
			if !ok {
				return
			}
			var completo map[string]json.RawMessage
			if err := json.Unmarshal(doc, &completo); err != nil {
				httpx.WriteErr(w, http.StatusBadGateway, "unreadable response from the node")
				return
			}
			httpx.WriteJSON(w, map[string]interface{}{"groups": json.RawMessage(completo["grupos"])})
			return
		}
		var gbody struct {
			Groups  []gameservers.Group `json:"groups"`
			Restart bool                `json:"restart"`
		}
		if err := httpx.DecodeBody(req.Body, &gbody); err != nil {
			httpx.WriteErr(w, http.StatusBadRequest, "invalid body")
			return
		}
		doc, ok := r.executaOp(w, req, srv, gameservers.OpSettingsPatch, map[string]interface{}{
			"servidor": srv.ID, "grupos": gbody.Groups, "reiniciar": gbody.Restart,
		}, true)
		if !ok {
			return
		}
		escreveBruto(w, doc)

	case "bans": // 27-case: `bans`
		if req.Method == http.MethodGet {
			doc, ok := r.executaOp(w, req, srv, gameservers.OpSettingsGet,
				map[string]interface{}{"servidor": srv.ID}, false)
			if !ok {
				return
			}
			var completo map[string]json.RawMessage
			if err := json.Unmarshal(doc, &completo); err != nil {
				httpx.WriteErr(w, http.StatusBadGateway, "unreadable response from the node")
				return
			}
			httpx.WriteJSON(w, map[string]interface{}{"bans": json.RawMessage(completo["banidos"])})
			return
		}
		var bbody struct {
			Bans []string `json:"bans"`
		}
		if err := httpx.DecodeBody(req.Body, &bbody); err != nil {
			httpx.WriteErr(w, http.StatusBadRequest, "invalid body")
			return
		}
		doc, ok := r.executaOp(w, req, srv, gameservers.OpSettingsPatch,
			map[string]interface{}{"servidor": srv.ID, "banidos": bbody.Bans}, true)
		if !ok {
			return
		}
		escreveBruto(w, doc)

	// ── configuration ────────────────────────────────────────────────────────

	case "server": // `server` — server options + default group
		if req.Method == http.MethodGet {
			doc, ok := r.executaOp(w, req, srv, gameservers.OpSettingsGet,
				map[string]interface{}{"servidor": srv.ID}, false)
			if !ok {
				return
			}
			var completo map[string]json.RawMessage
			if err := json.Unmarshal(doc, &completo); err != nil {
				httpx.WriteErr(w, http.StatusBadGateway, "unreadable response from the node")
				return
			}
			escreveBruto(w, completo["server"])
			return
		}
		var body struct {
			Server  map[string]interface{} `json:"server"`
			Group   map[string]interface{} `json:"group"`
			Restart bool                   `json:"restart"`
		}
		if err := httpx.DecodeBody(req.Body, &body); err != nil {
			httpx.WriteErr(w, http.StatusBadRequest, "invalid body")
			return
		}
		doc, ok := r.executaOp(w, req, srv, gameservers.OpSettingsPatch, map[string]interface{}{
			"servidor": srv.ID, "server": body.Server, "grupo": body.Group, "reiniciar": body.Restart,
		}, true)
		if !ok {
			return
		}
		escreveBruto(w, doc)

	case "settings": // 27-case: `settings`
		if req.Method == http.MethodGet {
			doc, ok := r.executaOp(w, req, srv, gameservers.OpSettingsGet,
				map[string]interface{}{"servidor": srv.ID}, false)
			if !ok {
				return
			}
			var completo map[string]json.RawMessage
			if err := json.Unmarshal(doc, &completo); err != nil {
				httpx.WriteErr(w, http.StatusBadGateway, "unreadable response from the node")
				return
			}
			escreveBruto(w, completo["jogo"])
			return
		}
		var body struct {
			Patch   map[string]interface{} `json:"patch"`
			Restart bool                   `json:"restart"`
		}
		if err := httpx.DecodeBody(req.Body, &body); err != nil {
			httpx.WriteErr(w, http.StatusBadRequest, "invalid body")
			return
		}
		if len(body.Patch) == 0 {
			httpx.WriteErr(w, http.StatusBadRequest, "empty patch")
			return
		}
		doc, ok := r.executaOp(w, req, srv, gameservers.OpSettingsPatch, map[string]interface{}{
			"servidor": srv.ID, "jogo": body.Patch, "reiniciar": body.Restart,
		}, true)
		if !ok {
			return
		}
		escreveBruto(w, doc)

	case "rawconfig": // 27-case: `rawconfig`
		// ⚠️ A DELIBERATE NARROWING. Writing FREE TEXT no longer exists on this
		// surface.
		//
		// The measured nuance, so that a review neither over- nor under-estimates
		// the earlier risk: the path was NEVER controlled by the client (it comes
		// from adapter.ConfigPath) and the content already went through unmarshal
		// plus a userGroups requirement. What changes is that ARBITRARY content in
		// a file the server executes as config is no longer possible — before
		// there was a single semantic guard; now there is a field allowlist.
		if req.Method == http.MethodGet {
			doc, ok := r.executaOp(w, req, srv, gameservers.OpSettingsGet,
				map[string]interface{}{"servidor": srv.ID}, false)
			if !ok {
				return
			}
			escreveBruto(w, doc)
			return
		}
		var cbody struct {
			Text    string                 `json:"text"`
			Server  map[string]interface{} `json:"server"`
			Group   map[string]interface{} `json:"group"`
			Jogo    map[string]interface{} `json:"jogo"`
			Restart bool                   `json:"restart"`
		}
		if err := httpx.DecodeBody(req.Body, &cbody); err != nil {
			httpx.WriteErr(w, http.StatusBadRequest, "invalid body")
			return
		}
		if cbody.Text != "" {
			httpx.WriteErr(w, http.StatusBadRequest,
				"free-text writes were removed: send enumerated fields in 'server', 'group' or 'jogo'")
			return
		}
		if len(cbody.Server) == 0 && len(cbody.Group) == 0 && len(cbody.Jogo) == 0 {
			httpx.WriteErr(w, http.StatusBadRequest, "no field to write")
			return
		}
		doc, ok := r.executaOp(w, req, srv, gameservers.OpSettingsPatch, map[string]interface{}{
			"servidor": srv.ID, "server": cbody.Server, "grupo": cbody.Group,
			"jogo": cbody.Jogo, "reiniciar": cbody.Restart,
		}, true)
		if !ok {
			return
		}
		escreveBruto(w, doc)

	case "runtime": // 27-case: `runtime`
		if req.Method == http.MethodGet {
			doc, ok := r.executaOp(w, req, srv, gameservers.OpRuntimeGet,
				map[string]interface{}{"servidor": srv.ID}, false)
			if !ok {
				return
			}
			escreveBruto(w, doc)
			return
		}
		var rbody struct {
			Patch map[string]string `json:"patch"`
		}
		if err := httpx.DecodeBody(req.Body, &rbody); err != nil {
			httpx.WriteErr(w, http.StatusBadRequest, "invalid body")
			return
		}
		doc, ok := r.executaOp(w, req, srv, gameservers.OpRuntimePatch,
			map[string]interface{}{"servidor": srv.ID, "patch": rbody.Patch}, true)
		if !ok {
			return
		}
		escreveBruto(w, doc)

	// ── telemetry ────────────────────────────────────────────────────────────

	case "history": // 27-case: `history`
		horas := 6
		if v := req.URL.Query().Get("hours"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				horas = n
			}
		}
		doc, ok := r.executaOp(w, req, srv, gameservers.OpHistoryList,
			map[string]interface{}{"servidor": srv.ID, "horas": horas}, false)
		if !ok {
			return
		}
		escreveBruto(w, doc)

	// ── trainer ──────────────────────────────────────────────────────────────

	case "trainer": // 27-case: `trainer`
		if req.Method == http.MethodGet {
			doc, ok := r.executaOp(w, req, srv, gameservers.OpTrainerStatus,
				map[string]interface{}{"servidor": srv.ID}, false)
			if !ok {
				return
			}
			escreveBruto(w, doc)
			return
		}
		if action == "apply" {
			doc, ok := r.executaOp(w, req, srv, gameservers.OpTrainerApply,
				map[string]interface{}{"servidor": srv.ID}, true)
			if !ok {
				return
			}
			escreveBruto(w, doc)
			return
		}
		var desejado gameservers.TrainerDesired
		if err := httpx.DecodeBody(req.Body, &desejado); err != nil {
			httpx.WriteErr(w, http.StatusBadRequest, "invalid body")
			return
		}
		doc, ok := r.executaOp(w, req, srv, gameservers.OpTrainerDesired, desejado, true)
		if !ok {
			return
		}
		escreveBruto(w, doc)

	// ── worlds ───────────────────────────────────────────────────────────────

	case "worlds": // `worlds` + 6 actions
		r.mundosDeJogo(w, req, srv, action)

	// ── backups ──────────────────────────────────────────────────────────────

	case "backups": // `backups` + 4 actions
		r.backupsDeJogo(w, req, srv, action)

	default:
		httpx.WriteErr(w, http.StatusNotFound, "unknown resource: "+resource)
	}
}

// mundosDeJogo covers the 7 `case`s of the world family.
func (r *Router) mundosDeJogo(w http.ResponseWriter, req *http.Request, srv gameservers.Server, action string) {
	switch action {
	case "": // 27-case: `worlds` ""
		doc, ok := r.executaOp(w, req, srv, gameservers.OpWorldList,
			map[string]interface{}{"servidor": srv.ID}, false)
		if !ok {
			return
		}
		escreveBruto(w, doc)

	case "switch": // 27-case: `worlds/switch`
		var body struct {
			World string `json:"world"`
		}
		if err := httpx.DecodeBody(req.Body, &body); err != nil {
			httpx.WriteErr(w, http.StatusBadRequest, "invalid body")
			return
		}
		doc, ok := r.executaOp(w, req, srv, gameservers.OpWorldSwitch,
			map[string]interface{}{"servidor": srv.ID, "mundo": body.World}, true)
		if !ok {
			return
		}
		escreveBruto(w, doc)

	case "export": // 27-case: `worlds/export`
		// Here is the structural difference: the handler does NOT receive a path.
		// It asks for the operation, gets an opaque Handle and tells the back-end
		// to open it. The one holding the disk is the node, from start to finish.
		nome := req.URL.Query().Get("world")
		doc, back, destino, ok := r.executaComBackend(w, req, srv, gameservers.OpWorldExport,
			map[string]interface{}{"servidor": srv.ID, "mundo": nome}, false)
		if !ok {
			return
		}
		entregaArtefato(w, req, back, destino, doc, nomeSeguroParaDownload(nome, "mundo")+".zip")

	case "import": // 27-case: `worlds/import`
		if _, ok := r.mustPrimary(w, req); !ok {
			return
		}
		if err := req.ParseMultipartForm(64 << 20); err != nil {
			httpx.WriteErr(w, http.StatusBadRequest, "invalid upload")
			return
		}
		arquivo, cab, err := req.FormFile("file")
		if err != nil {
			httpx.WriteErr(w, http.StatusBadRequest, "file is missing")
			return
		}
		defer arquivo.Close()
		if cab.Size > 600<<20 {
			httpx.WriteErr(w, http.StatusBadRequest, "file larger than 600 MB")
			return
		}
		back, destino, err := r.backendPara(srv)
		if err != nil {
			httpx.WriteErr(w, http.StatusBadRequest, err.Error())
			return
		}
		// The zip goes to the NODE and comes back as a Handle. The dashboard never
		// writes the file to a disk of its own: if it did, the path would start
		// crossing the boundary again on the next call.
		h, err := back.Receber(req.Context(), arquivo)
		if err != nil {
			codigo, msg := traduzErroDeNo(err, destino.Nome)
			httpx.WriteErr(w, codigo, msg)
			return
		}
		// The SAME back-end as the upload: the handle lives in its vault. Asking
		// for a new back-end here would lose the zip that has just been uploaded.
		env, _ := json.Marshal(map[string]interface{}{
			"servidor": srv.ID, "nome": req.FormValue("name"), "handle": string(h),
		})
		doc, err := back.Executar(req.Context(), gameservers.OpWorldImport, env)
		r.auditaJogo(req, destino.Nome, srv.ID, gameservers.OpWorldImport, err)
		if err != nil {
			codigo, msg := traduzErroDeNo(err, destino.Nome)
			httpx.WriteErr(w, codigo, msg)
			return
		}
		escreveBruto(w, doc)

	case "rename": // 27-case: `worlds/rename`
		var body struct {
			World string `json:"world"`
			Name  string `json:"name"`
		}
		if err := httpx.DecodeBody(req.Body, &body); err != nil {
			httpx.WriteErr(w, http.StatusBadRequest, "invalid body")
			return
		}
		doc, ok := r.executaOp(w, req, srv, gameservers.OpWorldRename,
			map[string]interface{}{"servidor": srv.ID, "de": body.World, "para": body.Name}, true)
		if !ok {
			return
		}
		escreveBruto(w, doc)

	case "duplicate": // 27-case: `worlds/duplicate`
		var body struct {
			World string `json:"world"`
			Name  string `json:"name"`
		}
		if err := httpx.DecodeBody(req.Body, &body); err != nil {
			httpx.WriteErr(w, http.StatusBadRequest, "invalid body")
			return
		}
		doc, ok := r.executaOp(w, req, srv, gameservers.OpWorldDuplicate,
			map[string]interface{}{"servidor": srv.ID, "de": body.World, "para": body.Name}, true)
		if !ok {
			return
		}
		escreveBruto(w, doc)

	case "delete": // 27-case: `worlds/delete`
		var body struct {
			World string `json:"world"`
		}
		if err := httpx.DecodeBody(req.Body, &body); err != nil {
			httpx.WriteErr(w, http.StatusBadRequest, "invalid body")
			return
		}
		doc, ok := r.executaOp(w, req, srv, gameservers.OpWorldDelete,
			map[string]interface{}{"servidor": srv.ID, "mundo": body.World}, true)
		if !ok {
			return
		}
		escreveBruto(w, doc)

	default:
		httpx.WriteErr(w, http.StatusNotFound, "unknown action on worlds: "+action)
	}
}

// backupsDeJogo covers the 5 `case`s of the backup family.
func (r *Router) backupsDeJogo(w http.ResponseWriter, req *http.Request, srv gameservers.Server, action string) {
	switch action {
	case "": // 27-case: `backups` ""
		doc, ok := r.executaOp(w, req, srv, gameservers.OpBackupList,
			map[string]interface{}{"servidor": srv.ID}, false)
		if !ok {
			return
		}
		escreveBruto(w, doc)

	case "create": // 27-case: `backups/create`
		doc, ok := r.executaOp(w, req, srv, gameservers.OpBackupCreate,
			map[string]interface{}{"servidor": srv.ID}, true)
		if !ok {
			return
		}
		escreveBruto(w, doc)

	case "restore": // 27-case: `backups/restore`
		var body struct {
			File string `json:"file"`
		}
		if err := httpx.DecodeBody(req.Body, &body); err != nil {
			httpx.WriteErr(w, http.StatusBadRequest, "invalid body")
			return
		}
		// The stop→restore→start sequence moved to the back-end, where the
		// container is. It used to live here and made three round trips; now it is
		// one single operation, and the network cannot interrupt it midway.
		doc, ok := r.executaOp(w, req, srv, gameservers.OpBackupRestore,
			map[string]interface{}{"servidor": srv.ID, "arquivo": body.File}, true)
		if !ok {
			return
		}
		escreveBruto(w, doc)

	case "download": // 27-case: `backups/download`
		arquivo := req.URL.Query().Get("file")
		doc, back, destino, ok := r.executaComBackend(w, req, srv, gameservers.OpBackupDownload,
			map[string]interface{}{"servidor": srv.ID, "arquivo": arquivo}, false)
		if !ok {
			return
		}
		entregaArtefato(w, req, back, destino, doc, nomeSeguroParaDownload(arquivo, "backup.zip"))

	default:
		httpx.WriteErr(w, http.StatusNotFound, "unknown action on backups: "+action)
	}
}

// entregaArtefato opens the Handle returned by an operation and streams the bytes.
//
// No path, no `http.ServeFile`: the artifact may be on another machine, and the
// dashboard only knows how to ask for it by token.
func entregaArtefato(
	w http.ResponseWriter, req *http.Request,
	back gameservers.Backend, destino gameservers.DestinoNo,
	doc json.RawMessage, nomeSugerido string,
) {
	var env struct {
		Handle string `json:"handle"`
	}
	if err := json.Unmarshal(doc, &env); err != nil || env.Handle == "" {
		httpx.WriteErr(w, http.StatusBadGateway, "the node did not return a reference for the file")
		return
	}
	rc, err := back.Abrir(req.Context(), gameservers.Handle(env.Handle))
	if err != nil {
		codigo, msg := traduzErroDeNo(err, destino.Nome)
		httpx.WriteErr(w, codigo, msg)
		return
	}
	defer rc.Close()

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+nomeSugerido+`"`)
	if _, err := io.Copy(w, rc); err != nil {
		// The header has already gone out: writing an error body would produce a
		// corrupted file that LOOKS complete. Only the log records it.
		return
	}
}
