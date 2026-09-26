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
// the server. Never a default, never a fallback to local. See ResolveTarget.

// NodeSource is the minimum the resolution needs to know about the inventory.
//
// An interface instead of `*inventory.Store` for the same reason as `NodeTarget`:
// `gameservers` is the package the `lab-agent` LINKS, and importing `inventory`
// would drag the Proxmox API client into the agent. The panel implements this
// interface over its own Store; the agent never needs to.
type NodeSource interface {
	// NoPorID returns the destination of an inventory node. The second return is
	// false when the ID does not exist — distinct from "exists and is incomplete".
	NoPorID(id string) (NodeTarget, bool)
}

// ResolveTarget says WHERE an operation on this server must go.
//
// Three refusals, all of them naming what is missing, because an error that
// names nothing forces the operator to guess which server is misregistered.
func ResolveTarget(s Server, source NodeSource) (NodeTarget, error) {
	if s.No == "" {
		// 🔴 NO DEFAULT HERE. Assuming "local" would make the operation happen on
		// the panel's machine — which for now is the VPS, and not the house.
		// A `backup.restore` like that writes in the wrong place and answers ok.
		return NodeTarget{}, fmt.Errorf(
			"server %q does not declare which node it lives on: register the 'node' field in the game inventory before operating",
			s.ID)
	}
	if source == nil {
		return NodeTarget{}, fmt.Errorf("server %q: node inventory unavailable", s.ID)
	}
	d, ok := source.NoPorID(s.No)
	if !ok {
		return NodeTarget{}, fmt.Errorf(
			"server %q points to node %q, which does not exist in the inventory", s.ID, s.No)
	}
	switch d.Transport {
	case TransportAgent, TransportPVEAPI, TransportSSH:
		return d, nil
	case "":
		return NodeTarget{}, fmt.Errorf(
			"node %q (of server %q) does not declare a transport: incomplete inventory", s.No, s.ID)
	default:
		return NodeTarget{}, fmt.Errorf(
			"node %q (of server %q) has an invalid transport: %q", s.No, s.ID, d.Transport)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// MIGRATION
//
// Cast in the mold of the inventory migration: idempotent, preserving unknown
// fields, atomic write, copy of the previous file before writing.

// preNodeBackupSuffix is the name of the copy kept before the first migration.
const preNodeBackupSuffix = ".pre-node.bak"

// MigrateInventoryToNode adds the node field to the records that do not have it
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
func MigrateInventoryToNode(path string) (changed bool, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// A missing inventory is not an error: the Manager already comes up empty
			// in that case (see New), and migrating what does not exist is a no-op.
			return false, nil
		}
		return false, err
	}

	var records []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &records); err != nil {
		return false, fmt.Errorf("invalid gameservers.json, migration aborted without writing: %w", err)
	}

	needs := false
	for _, r := range records {
		if _, has := r["node"]; !has {
			needs = true
			break
		}
	}
	if !needs {
		return false, nil
	}

	for _, r := range records {
		if _, has := r["node"]; !has {
			// Explicitly empty, not absent: that is what makes the inventory screen
			// show the field blank for the operator to fill in.
			r["node"] = json.RawMessage(`""`)
		}
	}

	output, err := serializeRecords(records)
	if err != nil {
		return false, err
	}

	// Copy of the previous file BEFORE writing. `writeAtomic` takes owner, mode
	// and durability from the reference file — here the reference is the inventory
	// itself, which is the correct owner.
	if err := writeAtomic(path+preNodeBackupSuffix, raw, path); err != nil {
		return false, fmt.Errorf("could not keep the previous copy, migration aborted: %w", err)
	}
	if err := writeAtomic(path, output, path); err != nil {
		return false, err
	}
	return true, nil
}

// serializeRecords writes with keys in a stable order.
//
// A Go `map` has no order, and `json.Marshal` of a map sorts the keys — but here
// the records are maps of RawMessage, and assembling the object by hand is what
// guarantees that running the migration again produces exactly the same bytes.
// An unstable order would make idempotency impossible to assert byte for byte.
func serializeRecords(records []map[string]json.RawMessage) ([]byte, error) {
	var buf []byte
	buf = append(buf, '[')
	for i, r := range records {
		if i > 0 {
			buf = append(buf, ',')
		}
		buf = append(buf, '\n', ' ', ' ')
		keys := make([]string, 0, len(r))
		for k := range r {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf = append(buf, '{')
		for j, k := range keys {
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
	if len(records) > 0 {
		buf = append(buf, '\n')
	}
	buf = append(buf, ']', '\n')
	if !json.Valid(buf) {
		return nil, fmt.Errorf("migration produced invalid JSON, nothing was written")
	}
	return buf, nil
}
