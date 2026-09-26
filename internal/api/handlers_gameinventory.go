package api

// Inventario de servidores de jogo (gameservers.json) editavel pela pagina.

import (
	"net/http"

	"server-control-panel/internal/gameservers"
	"server-control-panel/internal/httpx"
)

func (r *Router) handleGameInventory(w http.ResponseWriter, req *http.Request) {
	if r.gameMgr == nil {
		httpx.WriteErr(w, http.StatusServiceUnavailable, "game manager unavailable")
		return
	}
	if req.Method == http.MethodGet {
		httpx.WriteJSON(w, map[string]interface{}{"servers": r.gameMgr.List()})
		return
	}
	if _, ok := r.mustPrimary(w, req); !ok {
		return
	}
	var body struct {
		Servers []gameservers.Server `json:"servers"`
	}
	if err := httpx.DecodeBody(req.Body, &body); err != nil {
		httpx.WriteErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	// Refuse registration without a node, with the next step written into the message.
	//
	// Worth recording here and not only at operation time: an inventory that accepts
	// an incomplete record only reveals the problem the moment someone hits
	// "restart" — far from the registration that caused it. Refusing at write time puts
	// the error next to the blank field.
	for _, s := range body.Servers {
		if s.No == "" {
			httpx.WriteErr(w, http.StatusBadRequest,
				"server '"+s.ID+"' has no declared node: choose which node it lives on (field 'node') before saving — "+
					"operating without it would send the operation to a node picked by default")
			return
		}
	}
	if err := r.gameMgr.SaveInventory(body.Servers); err != nil {
		httpx.WriteErr(w, http.StatusBadRequest, err.Error())
		return
	}
	httpx.WriteJSON(w, map[string]interface{}{"ok": true})
}
