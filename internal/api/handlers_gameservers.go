package api

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

const maxGameWorldImportBytes = 600<<20 + 1<<20

func init() {
	httpmw.RegisterLargeBody(isGameWorldImportUpload, maxGameWorldImportBytes)
}

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

func (r *Router) handleGameServers(w http.ResponseWriter, req *http.Request) {
	if r.gameMgr == nil {
		httpx.WriteErr(w, http.StatusServiceUnavailable, "game manager unavailable")
		return
	}
	if req.Method != http.MethodGet {
		httpx.WriteErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	output := []map[string]interface{}{}
	for _, s := range r.gameMgr.List() {
		item := map[string]interface{}{"id": s.ID, "name": s.Name, "game": s.Game, "node": s.No}

		back, dest, err := r.backendFor(s)
		if err != nil {
			item["err"] = err.Error()
			output = append(output, item)
			continue
		}
		env, _ := json.Marshal(map[string]string{"server_id": s.ID})
		doc, err := back.Execute(req.Context(), gameservers.OpServerStatus, env)
		if err != nil {
			_, msg := translateNodeError(err, dest.Name)
			item["err"] = msg
			output = append(output, item)
			continue
		}
		var full map[string]interface{}
		if err := json.Unmarshal(doc, &full); err != nil {
			item["err"] = "unreadable response from node '" + dest.Name + "'"
			output = append(output, item)
			continue
		}
		for k, v := range full {
			item[k] = v
		}
		output = append(output, item)
	}
	httpx.WriteJSON(w, map[string]interface{}{"servers": output})
}

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

	srv, ok := r.gameMgr.Get(id)
	if !ok {
		httpx.WriteErr(w, http.StatusNotFound, "server '"+id+"' not found")
		return
	}

	switch resource {

	case "action":
		var body struct {
			Action string `json:"action"`
		}
		if err := httpx.DecodeBody(req.Body, &body); err != nil {
			httpx.WriteErr(w, http.StatusBadRequest, "invalid body")
			return
		}
		verb := gameservers.Verb(body.Action)
		if !gameservers.ValidVerbs[verb] {
			httpx.WriteErr(w, http.StatusBadRequest, "invalid action")
			return
		}
		doc, ok := r.execOp(w, req, srv, gameservers.OpServerAction,
			map[string]interface{}{"server_id": srv.ID, "verb": string(verb)}, true)
		if !ok {
			return
		}
		writeRaw(w, doc)

	case "update":
		doc, ok := r.execOp(w, req, srv, gameservers.OpServerAction,
			map[string]interface{}{"server_id": srv.ID, "verb": string(gameservers.VerbUpdate)}, true)
		if !ok {
			return
		}
		writeRaw(w, doc)

	case "logs":
		tail := req.URL.Query().Get("tail")
		if tail == "" {
			tail = "200"
		}
		doc, ok := r.execOp(w, req, srv, gameservers.OpServerLogs,
			map[string]interface{}{"server_id": srv.ID, "tail": tail}, false)
		if !ok {
			return
		}
		writeRaw(w, doc)

	case "connection", "build":
		doc, ok := r.execOp(w, req, srv, gameservers.OpServerStatus,
			map[string]interface{}{"server_id": srv.ID}, false)
		if !ok {
			return
		}
		var full map[string]json.RawMessage
		if err := json.Unmarshal(doc, &full); err != nil {
			httpx.WriteErr(w, http.StatusBadGateway, "unreadable response from the node")
			return
		}
		writeRaw(w, full[resource])

	case "groups":
		if req.Method == http.MethodGet {
			doc, ok := r.execOp(w, req, srv, gameservers.OpSettingsGet,
				map[string]interface{}{"server_id": srv.ID}, false)
			if !ok {
				return
			}
			var full map[string]json.RawMessage
			if err := json.Unmarshal(doc, &full); err != nil {
				httpx.WriteErr(w, http.StatusBadGateway, "unreadable response from the node")
				return
			}
			httpx.WriteJSON(w, map[string]interface{}{"groups": json.RawMessage(full["groups"])})
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
		doc, ok := r.execOp(w, req, srv, gameservers.OpSettingsPatch, map[string]interface{}{
			"server_id": srv.ID, "groups": gbody.Groups, "restart": gbody.Restart,
		}, true)
		if !ok {
			return
		}
		writeRaw(w, doc)

	case "bans":
		if req.Method == http.MethodGet {
			doc, ok := r.execOp(w, req, srv, gameservers.OpSettingsGet,
				map[string]interface{}{"server_id": srv.ID}, false)
			if !ok {
				return
			}
			var full map[string]json.RawMessage
			if err := json.Unmarshal(doc, &full); err != nil {
				httpx.WriteErr(w, http.StatusBadGateway, "unreadable response from the node")
				return
			}
			httpx.WriteJSON(w, map[string]interface{}{"bans": json.RawMessage(full["banned"])})
			return
		}
		var bbody struct {
			Bans []string `json:"bans"`
		}
		if err := httpx.DecodeBody(req.Body, &bbody); err != nil {
			httpx.WriteErr(w, http.StatusBadRequest, "invalid body")
			return
		}
		doc, ok := r.execOp(w, req, srv, gameservers.OpSettingsPatch,
			map[string]interface{}{"server_id": srv.ID, "banned": bbody.Bans}, true)
		if !ok {
			return
		}
		writeRaw(w, doc)

	case "server":
		if req.Method == http.MethodGet {
			doc, ok := r.execOp(w, req, srv, gameservers.OpSettingsGet,
				map[string]interface{}{"server_id": srv.ID}, false)
			if !ok {
				return
			}
			var full map[string]json.RawMessage
			if err := json.Unmarshal(doc, &full); err != nil {
				httpx.WriteErr(w, http.StatusBadGateway, "unreadable response from the node")
				return
			}
			writeRaw(w, full["server"])
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
		doc, ok := r.execOp(w, req, srv, gameservers.OpSettingsPatch, map[string]interface{}{
			"server_id": srv.ID, "server": body.Server, "group": body.Group, "restart": body.Restart,
		}, true)
		if !ok {
			return
		}
		writeRaw(w, doc)

	case "settings":
		if req.Method == http.MethodGet {
			doc, ok := r.execOp(w, req, srv, gameservers.OpSettingsGet,
				map[string]interface{}{"server_id": srv.ID}, false)
			if !ok {
				return
			}
			var full map[string]json.RawMessage
			if err := json.Unmarshal(doc, &full); err != nil {
				httpx.WriteErr(w, http.StatusBadGateway, "unreadable response from the node")
				return
			}
			writeRaw(w, full["game"])
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
		doc, ok := r.execOp(w, req, srv, gameservers.OpSettingsPatch, map[string]interface{}{
			"server_id": srv.ID, "game": body.Patch, "restart": body.Restart,
		}, true)
		if !ok {
			return
		}
		writeRaw(w, doc)

	case "rawconfig":
		if req.Method == http.MethodGet {
			doc, ok := r.execOp(w, req, srv, gameservers.OpSettingsGet,
				map[string]interface{}{"server_id": srv.ID}, false)
			if !ok {
				return
			}
			writeRaw(w, doc)
			return
		}
		var cbody struct {
			Text    string                 `json:"text"`
			Server  map[string]interface{} `json:"server"`
			Group   map[string]interface{} `json:"group"`
			Game    map[string]interface{} `json:"game"`
			Restart bool                   `json:"restart"`
		}
		if err := httpx.DecodeBody(req.Body, &cbody); err != nil {
			httpx.WriteErr(w, http.StatusBadRequest, "invalid body")
			return
		}
		if cbody.Text != "" {
			httpx.WriteErr(w, http.StatusBadRequest,
				"free-text writes were removed: send enumerated fields in 'server', 'group' or 'game'")
			return
		}
		if len(cbody.Server) == 0 && len(cbody.Group) == 0 && len(cbody.Game) == 0 {
			httpx.WriteErr(w, http.StatusBadRequest, "no field to write")
			return
		}
		doc, ok := r.execOp(w, req, srv, gameservers.OpSettingsPatch, map[string]interface{}{
			"server_id": srv.ID, "server": cbody.Server, "group": cbody.Group,
			"game": cbody.Game, "restart": cbody.Restart,
		}, true)
		if !ok {
			return
		}
		writeRaw(w, doc)

	case "runtime":
		if req.Method == http.MethodGet {
			doc, ok := r.execOp(w, req, srv, gameservers.OpRuntimeGet,
				map[string]interface{}{"server_id": srv.ID}, false)
			if !ok {
				return
			}
			writeRaw(w, doc)
			return
		}
		var rbody struct {
			Patch map[string]string `json:"patch"`
		}
		if err := httpx.DecodeBody(req.Body, &rbody); err != nil {
			httpx.WriteErr(w, http.StatusBadRequest, "invalid body")
			return
		}
		doc, ok := r.execOp(w, req, srv, gameservers.OpRuntimePatch,
			map[string]interface{}{"server_id": srv.ID, "patch": rbody.Patch}, true)
		if !ok {
			return
		}
		writeRaw(w, doc)

	case "history":
		hours := 6
		if v := req.URL.Query().Get("hours"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				hours = n
			}
		}
		doc, ok := r.execOp(w, req, srv, gameservers.OpHistoryList,
			map[string]interface{}{"server_id": srv.ID, "hours": hours}, false)
		if !ok {
			return
		}
		writeRaw(w, doc)

	case "trainer":
		if req.Method == http.MethodGet {
			doc, ok := r.execOp(w, req, srv, gameservers.OpTrainerStatus,
				map[string]interface{}{"server_id": srv.ID}, false)
			if !ok {
				return
			}
			writeRaw(w, doc)
			return
		}
		if action == "apply" {
			doc, ok := r.execOp(w, req, srv, gameservers.OpTrainerApply,
				map[string]interface{}{"server_id": srv.ID}, true)
			if !ok {
				return
			}
			writeRaw(w, doc)
			return
		}
		var desired gameservers.TrainerDesired
		if err := httpx.DecodeBody(req.Body, &desired); err != nil {
			httpx.WriteErr(w, http.StatusBadRequest, "invalid body")
			return
		}
		doc, ok := r.execOp(w, req, srv, gameservers.OpTrainerDesired, desired, true)
		if !ok {
			return
		}
		writeRaw(w, doc)

	case "worlds":
		r.gameWorlds(w, req, srv, action)

	case "backups":
		r.gameBackups(w, req, srv, action)

	default:
		httpx.WriteErr(w, http.StatusNotFound, "unknown resource: "+resource)
	}
}

func (r *Router) gameWorlds(w http.ResponseWriter, req *http.Request, srv gameservers.Server, action string) {
	switch action {
	case "":
		doc, ok := r.execOp(w, req, srv, gameservers.OpWorldList,
			map[string]interface{}{"server_id": srv.ID}, false)
		if !ok {
			return
		}
		writeRaw(w, doc)

	case "switch":
		var body struct {
			World string `json:"world"`
		}
		if err := httpx.DecodeBody(req.Body, &body); err != nil {
			httpx.WriteErr(w, http.StatusBadRequest, "invalid body")
			return
		}
		doc, ok := r.execOp(w, req, srv, gameservers.OpWorldSwitch,
			map[string]interface{}{"server_id": srv.ID, "world": body.World}, true)
		if !ok {
			return
		}
		writeRaw(w, doc)

	case "export":
		name := req.URL.Query().Get("world")
		doc, back, dest, ok := r.execWithBackend(w, req, srv, gameservers.OpWorldExport,
			map[string]interface{}{"server_id": srv.ID, "world": name}, false)
		if !ok {
			return
		}
		deliverArtifact(w, req, back, dest, doc, safeDownloadName(name, "world")+".zip")

	case "import":
		if _, ok := r.mustPrimary(w, req); !ok {
			return
		}
		if err := req.ParseMultipartForm(64 << 20); err != nil {
			httpx.WriteErr(w, http.StatusBadRequest, "invalid upload")
			return
		}
		file, cab, err := req.FormFile("file")
		if err != nil {
			httpx.WriteErr(w, http.StatusBadRequest, "file is missing")
			return
		}
		defer file.Close()
		if cab.Size > 600<<20 {
			httpx.WriteErr(w, http.StatusBadRequest, "file larger than 600 MB")
			return
		}
		back, dest, err := r.backendFor(srv)
		if err != nil {
			httpx.WriteErr(w, http.StatusBadRequest, err.Error())
			return
		}
		h, err := back.Receive(req.Context(), file)
		if err != nil {
			code, msg := translateNodeError(err, dest.Name)
			httpx.WriteErr(w, code, msg)
			return
		}
		env, _ := json.Marshal(map[string]interface{}{
			"server_id": srv.ID, "name": req.FormValue("name"), "handle": string(h),
		})
		doc, err := back.Execute(req.Context(), gameservers.OpWorldImport, env)
		r.auditGame(req, dest.Name, srv.ID, gameservers.OpWorldImport, err)
		if err != nil {
			code, msg := translateNodeError(err, dest.Name)
			httpx.WriteErr(w, code, msg)
			return
		}
		writeRaw(w, doc)

	case "rename":
		var body struct {
			World string `json:"world"`
			Name  string `json:"name"`
		}
		if err := httpx.DecodeBody(req.Body, &body); err != nil {
			httpx.WriteErr(w, http.StatusBadRequest, "invalid body")
			return
		}
		doc, ok := r.execOp(w, req, srv, gameservers.OpWorldRename,
			map[string]interface{}{"server_id": srv.ID, "from": body.World, "to": body.Name}, true)
		if !ok {
			return
		}
		writeRaw(w, doc)

	case "duplicate":
		var body struct {
			World string `json:"world"`
			Name  string `json:"name"`
		}
		if err := httpx.DecodeBody(req.Body, &body); err != nil {
			httpx.WriteErr(w, http.StatusBadRequest, "invalid body")
			return
		}
		doc, ok := r.execOp(w, req, srv, gameservers.OpWorldDuplicate,
			map[string]interface{}{"server_id": srv.ID, "from": body.World, "to": body.Name}, true)
		if !ok {
			return
		}
		writeRaw(w, doc)

	case "delete":
		var body struct {
			World string `json:"world"`
		}
		if err := httpx.DecodeBody(req.Body, &body); err != nil {
			httpx.WriteErr(w, http.StatusBadRequest, "invalid body")
			return
		}
		doc, ok := r.execOp(w, req, srv, gameservers.OpWorldDelete,
			map[string]interface{}{"server_id": srv.ID, "world": body.World}, true)
		if !ok {
			return
		}
		writeRaw(w, doc)

	default:
		httpx.WriteErr(w, http.StatusNotFound, "unknown action on worlds: "+action)
	}
}

func (r *Router) gameBackups(w http.ResponseWriter, req *http.Request, srv gameservers.Server, action string) {
	switch action {
	case "":
		doc, ok := r.execOp(w, req, srv, gameservers.OpBackupList,
			map[string]interface{}{"server_id": srv.ID}, false)
		if !ok {
			return
		}
		writeRaw(w, doc)

	case "create":
		doc, ok := r.execOp(w, req, srv, gameservers.OpBackupCreate,
			map[string]interface{}{"server_id": srv.ID}, true)
		if !ok {
			return
		}
		writeRaw(w, doc)

	case "restore":
		var body struct {
			File string `json:"file"`
		}
		if err := httpx.DecodeBody(req.Body, &body); err != nil {
			httpx.WriteErr(w, http.StatusBadRequest, "invalid body")
			return
		}
		doc, ok := r.execOp(w, req, srv, gameservers.OpBackupRestore,
			map[string]interface{}{"server_id": srv.ID, "file": body.File}, true)
		if !ok {
			return
		}
		writeRaw(w, doc)

	case "download":
		file := req.URL.Query().Get("file")
		doc, back, dest, ok := r.execWithBackend(w, req, srv, gameservers.OpBackupDownload,
			map[string]interface{}{"server_id": srv.ID, "file": file}, false)
		if !ok {
			return
		}
		deliverArtifact(w, req, back, dest, doc, safeDownloadName(file, "backup.zip"))

	default:
		httpx.WriteErr(w, http.StatusNotFound, "unknown action on backups: "+action)
	}
}

func deliverArtifact(
	w http.ResponseWriter, req *http.Request,
	back gameservers.Backend, dest gameservers.NodeTarget,
	doc json.RawMessage, suggestedName string,
) {
	var env struct {
		Handle string `json:"handle"`
	}
	if err := json.Unmarshal(doc, &env); err != nil || env.Handle == "" {
		httpx.WriteErr(w, http.StatusBadGateway, "the node did not return a reference for the file")
		return
	}
	rc, err := back.Open(req.Context(), gameservers.Handle(env.Handle))
	if err != nil {
		code, msg := translateNodeError(err, dest.Name)
		httpx.WriteErr(w, code, msg)
		return
	}
	defer rc.Close()

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+suggestedName+`"`)
	if _, err := io.Copy(w, rc); err != nil {
		return
	}
}
