package gameservers

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Editing the SERVER options (outside gameSettings): name, slots, password,
// default group permissions, voice/text chat and tags.
//
// The infra care that makes this possible: the Docker image reapplies
// SERVER_NAME/SERVER_PASSWORD/SERVER_SLOT_COUNT from the compose on top of the
// JSON on every start. Those three env vars were removed from the compose
// precisely so that enshrouded_server.json is the single source — otherwise the
// panel would write and the next restart would silently revert it.

// serverFields lists what may be edited at the top of the config, with the
// expected type. A field outside this list is refused — it keeps the UI from
// writing garbage (or someone from pointing saveDirectory somewhere else).
var serverFields = map[string]string{
	"name":               "string",
	"slotCount":          "int",
	"enableVoiceChat":    "bool",
	"enableTextChat":     "bool",
	"voiceChatMode":      "string",
	"gameSettingsPreset": "string",
}

// groupFields are the default group's permissions (userGroups[0]).
var groupFields = map[string]string{
	"name":                 "string",
	"password":             "string",
	"canKickBan":           "bool",
	"canAccessInventories": "bool",
	"canEditWorld":         "bool",
	"canEditBase":          "bool",
	"canExtendBase":        "bool",
	"reservedSlots":        "int",
}

// ServerSettings returns the server options plus the default group.
func (enshrouded) ServerSettings(s Server) (map[string]interface{}, error) {
	cfg, err := readJSONFile(enshConfigPath(s))
	if err != nil {
		return nil, err
	}
	srv := map[string]interface{}{}
	for k := range serverFields {
		if v, ok := cfg[k]; ok {
			srv[k] = v
		}
	}
	// Read-only: touching this from the panel would break the container.
	srv["_readonly"] = map[string]interface{}{
		"ip": cfg["ip"], "queryPort": cfg["queryPort"],
		"saveDirectory": cfg["saveDirectory"], "logDirectory": cfg["logDirectory"],
	}

	grp := map[string]interface{}{}
	if groups, ok := cfg["userGroups"].([]interface{}); ok && len(groups) > 0 {
		if g0, ok := groups[0].(map[string]interface{}); ok {
			for k := range groupFields {
				if v, ok := g0[k]; ok {
					grp[k] = v
				}
			}
		}
	}
	return map[string]interface{}{"server": srv, "group": grp}, nil
}

func coerce(field, kind string, v interface{}) (interface{}, error) {
	switch kind {
	case "string":
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("%s: expected text", field)
		}
		s = strings.TrimSpace(s)
		if s == "" {
			return nil, fmt.Errorf("%s: cannot be empty", field)
		}
		if len(s) > 120 {
			return nil, fmt.Errorf("%s: at most 120 characters", field)
		}
		return s, nil
	case "bool":
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("%s: expected yes/no", field)
		}
		return b, nil
	case "int":
		f, ok := v.(float64) // JSON number
		if !ok {
			return nil, fmt.Errorf("%s: expected a number", field)
		}
		n := int(f)
		if float64(n) != f {
			return nil, fmt.Errorf("%s: must be an integer", field)
		}
		if field == "slotCount" && (n < 1 || n > 16) {
			return nil, fmt.Errorf("slotCount: the game accepts 1 to 16 slots")
		}
		if field == "reservedSlots" && n < 0 {
			return nil, fmt.Errorf("reservedSlots: cannot be negative")
		}
		return n, nil
	}
	return nil, fmt.Errorf("%s: unknown type", field)
}

// SaveServerSettings applies patches at the top of the config and to the default
// group. It writes atomically, like the SaveSettings of gameSettings.
func (enshrouded) SaveServerSettings(s Server, srvPatch, grpPatch map[string]interface{}) error {
	if len(srvPatch) == 0 && len(grpPatch) == 0 {
		return fmt.Errorf("nothing to save")
	}
	path := enshConfigPath(s)
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return fmt.Errorf("invalid enshrouded_server.json: %w", err)
	}

	for k, v := range srvPatch {
		kind, ok := serverFields[k]
		if !ok {
			return fmt.Errorf("server field not editable: %s", k)
		}
		cv, err := coerce(k, kind, v)
		if err != nil {
			return err
		}
		cfg[k] = cv
	}

	if len(grpPatch) > 0 {
		groups, _ := cfg["userGroups"].([]interface{})
		if len(groups) == 0 {
			return fmt.Errorf("config has no userGroups — nowhere to write")
		}
		g0, ok := groups[0].(map[string]interface{})
		if !ok {
			return fmt.Errorf("userGroups[0] in an unexpected format")
		}
		for k, v := range grpPatch {
			kind, ok := groupFields[k]
			if !ok {
				return fmt.Errorf("permission not editable: %s", k)
			}
			cv, err := coerce(k, kind, v)
			if err != nil {
				return err
			}
			g0[k] = cv
		}
		groups[0] = g0
		cfg["userGroups"] = groups
	}

	out, err := json.MarshalIndent(cfg, "", "    ")
	if err != nil {
		return err
	}
	// It preserved the MODE, lost the OWNER. See escreveAtomico in fsatomic.go.
	return escreveAtomico(path, out, s.Root)
}

// RenameWorld renames the world's FOLDER (the label the panel uses).
//
// The name that shows up inside the game lives compressed in the save (KSC1+ZSTD
// → BDB1) and is NOT touched here: editing that blob is exactly what proved
// unfeasible in the save-editor project. Renaming the folder is safe and reversible.
func (e enshrouded) RenameWorld(s Server, old, novo string) error {
	if err := safeName(old); err != nil {
		return err
	}
	if err := safeName(novo); err != nil {
		return err
	}
	from := filepath.Join(s.Root, "worlds", old)
	to := filepath.Join(s.Root, "worlds", novo)
	if _, err := os.Stat(from); err != nil {
		return fmt.Errorf("world '%s' does not exist", old)
	}
	if _, err := os.Stat(to); err == nil {
		return fmt.Errorf("a world named '%s' already exists", novo)
	}
	if err := os.Rename(from, to); err != nil {
		return err
	}
	// If it was the active one, the pointer has to follow — otherwise the next
	// switch would try to archive into a directory that no longer exists.
	if e.ActiveWorld(s) == old {
		// THE SNAPSHOT OF THE DEFECT IN PRODUCTION: this was the worst of the sites
		// — a raw `os.WriteFile`, with no Chmod and no Chown. It is what produced
		// /opt/enshrouded/.active as root:root inside a 4711:4711 tree.
		// `ref` is s.Root because .active may not exist yet.
		return escreveAtomico(filepath.Join(s.Root, ".active"), []byte(novo+"\n"), s.Root)
	}
	return nil
}
