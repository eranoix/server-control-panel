package gameservers

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

type NodeSource interface {
	NodeByID(id string) (NodeTarget, bool)
}

func ResolveTarget(s Server, source NodeSource) (NodeTarget, error) {
	if s.No == "" {
		return NodeTarget{}, fmt.Errorf(
			"server %q does not declare which node it lives on: register the 'node' field in the game inventory before operating",
			s.ID)
	}
	if source == nil {
		return NodeTarget{}, fmt.Errorf("server %q: node inventory unavailable", s.ID)
	}
	d, ok := source.NodeByID(s.No)
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

const preNodeBackupSuffix = ".pre-node.bak"

func MigrateInventoryToNode(path string) (changed bool, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
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
			r["node"] = json.RawMessage(`""`)
		}
	}

	output, err := serializeRecords(records)
	if err != nil {
		return false, err
	}

	if err := writeAtomic(path+preNodeBackupSuffix, raw, path); err != nil {
		return false, fmt.Errorf("could not keep the previous copy, migration aborted: %w", err)
	}
	if err := writeAtomic(path, output, path); err != nil {
		return false, err
	}
	return true, nil
}

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
			name, err := json.Marshal(k)
			if err != nil {
				return nil, err
			}
			buf = append(buf, name...)
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
