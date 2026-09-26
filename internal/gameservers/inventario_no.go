package gameservers

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// The game inventory now says WHICH NODE each server lives on.
//
// # WHY THIS IS NOT A REGISTRATION DETAIL
//
// An agent that answers perfectly is worth nothing if the panel asks the wrong
// node. And "wrong node" here is not a blank screen: it is `world.switch`
// running on the family's save, or `backup.restore` overwriting another
// server's world. The damage is to real data and there is no undo.
//
// Measured in the field: the VPS panel's `data/gameservers.json` is EMPTY, and
// the other fork's points at a path that no longer exists. That is, the field
// is born with no record to fill in — and that is precisely why the behavior on
// absence matters so much: for a while, absence is the rule.
//
// THE HARD RULE: a server with no declared node makes the operation FAIL, naming
// the server. Never a default, never a fallback to local. See ResolverDestino.

// FonteDeNos is the minimum the resolution needs to know about the inventory.
//
// An interface instead of `*inventory.Store` for the same reason as `DestinoNo`:
// `gameservers` is the package the `lab-agent` LINKS, and importing `inventory`
// would drag the Proxmox API client into the agent. The panel implements this
// interface over its own Store; the agent never needs to.
type FonteDeNos interface {
	// NoPorID returns the destination of an inventory node. The second return is
	// false when the ID does not exist — distinct from "exists and is incomplete".
	NoPorID(id string) (DestinoNo, bool)
}

// ResolverDestino says WHERE an operation on this server must go.
//
// Three refusals, all of them naming what is missing, because an error that
// names nothing forces the operator to guess which server is misregistered.
func ResolverDestino(s Server, fonte FonteDeNos) (DestinoNo, error) {
	if s.No == "" {
		// 🔴 NO DEFAULT HERE. Assuming "local" would make the operation happen on
		// the panel's machine — which for now is the VPS, and not the house.
		// A `backup.restore` like that writes in the wrong place and answers ok.
		return DestinoNo{}, fmt.Errorf(
			"server %q does not declare which node it lives on: register the 'node' field in the game inventory before operating",
			s.ID)
	}
	if fonte == nil {
		return DestinoNo{}, fmt.Errorf("server %q: node inventory unavailable", s.ID)
	}
	d, ok := fonte.NoPorID(s.No)
	if !ok {
		return DestinoNo{}, fmt.Errorf(
			"server %q points to node %q, which does not exist in the inventory", s.ID, s.No)
	}
	switch d.Transport {
	case TransporteAgente, TransportePVEAPI, TransporteSSH:
		return d, nil
	case "":
		return DestinoNo{}, fmt.Errorf(
			"node %q (of server %q) does not declare a transport: incomplete inventory", s.No, s.ID)
	default:
		return DestinoNo{}, fmt.Errorf(
			"node %q (of server %q) has an invalid transport: %q", s.No, s.ID, d.Transport)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// MIGRATION
//
// Cast in the mold of the inventory migration: idempotent, preserving unknown
// fields, atomic write, copy of the previous file before writing.

// sufixoCopiaPreNo is the name of the copy kept before the first migration.
const sufixoCopiaPreNo = ".pre-node.bak"

// MigrarInventarioParaNo adds the node field to the records that do not have it
// yet.
//
// It works over `map[string]json.RawMessage`, NOT over []Server, and that
// difference is the whole reason the unknown-fields test exists: decoding into
// the struct and re-serializing would ERASE any field this binary does not know
// about. A migration that loses a field is silent data loss — the operator only
// finds out when the screen that used that field stops working.
//
// Idempotent BY CONSTRUCTION, not by coincidence: if no record needs the field,
// the function writes nothing. A second run does not touch the file, and so has
// no way of producing different bytes.
func MigrarInventarioParaNo(caminho string) (mudou bool, err error) {
	bruto, err := os.ReadFile(caminho)
	if err != nil {
		if os.IsNotExist(err) {
			// A missing inventory is not an error: the Manager already comes up empty
			// in that case (see New), and migrating what does not exist is a no-op.
			return false, nil
		}
		return false, err
	}

	var registros []map[string]json.RawMessage
	if err := json.Unmarshal(bruto, &registros); err != nil {
		return false, fmt.Errorf("invalid gameservers.json, migration aborted without writing: %w", err)
	}

	precisa := false
	for _, r := range registros {
		if _, tem := r["node"]; !tem {
			precisa = true
			break
		}
	}
	if !precisa {
		return false, nil
	}

	for _, r := range registros {
		if _, tem := r["node"]; !tem {
			// Explicitly empty, not absent: that is what makes the inventory screen
			// show the field blank for the operator to fill in.
			r["node"] = json.RawMessage(`""`)
		}
	}

	saida, err := serializaRegistros(registros)
	if err != nil {
		return false, err
	}

	// Copy of the previous file BEFORE writing. `escreveAtomico` takes owner, mode
	// and durability from the reference file — here the reference is the inventory
	// itself, which is the correct owner.
	if err := escreveAtomico(caminho+sufixoCopiaPreNo, bruto, caminho); err != nil {
		return false, fmt.Errorf("could not keep the previous copy, migration aborted: %w", err)
	}
	if err := escreveAtomico(caminho, saida, caminho); err != nil {
		return false, err
	}
	return true, nil
}

// serializaRegistros writes with keys in a stable order.
//
// A Go `map` has no order, and `json.Marshal` of a map sorts the keys — but here
// the records are maps of RawMessage, and assembling the object by hand is what
// guarantees that running the migration again produces exactly the same bytes.
// An unstable order would make idempotency impossible to assert byte for byte.
func serializaRegistros(registros []map[string]json.RawMessage) ([]byte, error) {
	var buf []byte
	buf = append(buf, '[')
	for i, r := range registros {
		if i > 0 {
			buf = append(buf, ',')
		}
		buf = append(buf, '\n', ' ', ' ')
		chaves := make([]string, 0, len(r))
		for k := range r {
			chaves = append(chaves, k)
		}
		sort.Strings(chaves)
		buf = append(buf, '{')
		for j, k := range chaves {
			if j > 0 {
				buf = append(buf, ',')
			}
			nome, err := json.Marshal(k)
			if err != nil {
				return nil, err
			}
			buf = append(buf, nome...)
			buf = append(buf, ':')
			buf = append(buf, r[k]...)
		}
		buf = append(buf, '}')
	}
	if len(registros) > 0 {
		buf = append(buf, '\n')
	}
	buf = append(buf, ']', '\n')
	if !json.Valid(buf) {
		return nil, fmt.Errorf("migration produced invalid JSON, nothing was written")
	}
	return buf, nil
}
