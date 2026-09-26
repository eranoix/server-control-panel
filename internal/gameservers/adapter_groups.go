package gameservers

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Privilege groups and the ban list.
//
// Enshrouded has no RCON: what rules over what each player may do is the group
// they fall into, and the player picks the group by typing that group's PASSWORD
// on joining. Hence the community's Admin/Friend/Guest pattern.
//
// A critical rule documented by the community and handled here in validation:
// a password repeated across groups TAKES THE SERVER DOWN at boot. Since that
// would only surface as "the server does not come up" after saving, the check
// happens before writing.

// Group is a server role.
type Group struct {
	Name                 string `json:"name"`
	Password             string `json:"password"`
	CanKickBan           bool   `json:"canKickBan"`
	CanAccessInventories bool   `json:"canAccessInventories"`
	CanEditWorld         bool   `json:"canEditWorld"`
	CanEditBase          bool   `json:"canEditBase"`
	CanExtendBase        bool   `json:"canExtendBase"`
	ReservedSlots        int    `json:"reservedSlots"`
}

// Groups reads every group out of the config.
func (enshrouded) Groups(s Server) ([]Group, error) {
	cfg, err := readJSONFile(enshConfigPath(s))
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(cfg["userGroups"])
	var out []Group
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	if out == nil {
		out = []Group{}
	}
	return out, nil
}

func validateGroups(gs []Group, slotCount int) error {
	if len(gs) == 0 {
		return fmt.Errorf("at least one group is required")
	}
	if len(gs) > 12 {
		return fmt.Errorf("at most 12 groups")
	}
	names, pwds := map[string]bool{}, map[string]bool{}
	reserved := 0
	for i := range gs {
		g := &gs[i]
		g.Name = strings.TrimSpace(g.Name)
		g.Password = strings.TrimSpace(g.Password)
		if g.Name == "" {
			return fmt.Errorf("group %d: name cannot be empty", i+1)
		}
		if len(g.Name) > 40 {
			return fmt.Errorf("group '%s': name too long (max 40)", g.Name)
		}
		if g.Password == "" {
			return fmt.Errorf("group '%s': password cannot be empty — without a password nobody joins that role", g.Name)
		}
		if len(g.Password) < 4 {
			return fmt.Errorf("group '%s': password too short (min 4)", g.Name)
		}
		if names[strings.ToLower(g.Name)] {
			return fmt.Errorf("duplicate group name: '%s'", g.Name)
		}
		// THIS is the one that breaks the server at boot if it gets through.
		if pwds[g.Password] {
			return fmt.Errorf("duplicate password in group '%s' — identical passwords across groups bring the server down at startup", g.Name)
		}
		names[strings.ToLower(g.Name)] = true
		pwds[g.Password] = true

		if g.ReservedSlots < 0 {
			return fmt.Errorf("group '%s': reserved slots cannot be negative", g.Name)
		}
		reserved += g.ReservedSlots
	}
	if slotCount > 0 && reserved > slotCount {
		return fmt.Errorf("reserved slots add up to %d, more than the server's %d slots", reserved, slotCount)
	}
	return nil
}

// SaveGroups replaces the whole list of groups, validating before writing.
func (enshrouded) SaveGroups(s Server, gs []Group) error {
	path := enshConfigPath(s)
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return fmt.Errorf("invalid enshrouded_server.json: %w", err)
	}
	slots := 0
	if v, ok := cfg["slotCount"].(float64); ok {
		slots = int(v)
	}
	if err := validateGroups(gs, slots); err != nil {
		return err
	}

	raw, err := json.Marshal(gs)
	if err != nil {
		return err
	}
	var asAny []interface{}
	if err := json.Unmarshal(raw, &asAny); err != nil {
		return err
	}
	cfg["userGroups"] = asAny
	return writeJSONAtomic(path, cfg, s.Root)
}

// Bans reads the list of banned accounts.
func (enshrouded) Bans(s Server) ([]string, error) {
	cfg, err := readJSONFile(enshConfigPath(s))
	if err != nil {
		return nil, err
	}
	out := []string{}
	if arr, ok := cfg["bannedAccounts"].([]interface{}); ok {
		for _, v := range arr {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out, nil
}

// SaveBans replaces the ban list (one SteamID64 per line).
func (enshrouded) SaveBans(s Server, list []string) error {
	path := enshConfigPath(s)
	cfg, err := readJSONFile(path)
	if err != nil {
		return err
	}
	clean := []interface{}{}
	seen := map[string]bool{}
	for _, v := range list {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		if len(v) > 64 {
			return fmt.Errorf("identifier too long: %s", v[:24]+"…")
		}
		seen[v] = true
		clean = append(clean, v)
	}
	cfg["bannedAccounts"] = clean
	return writeJSONAtomic(path, cfg, s.Root)
}

// writeJSONAtomic becomes a thin shell over escreveAtomico: it only marshals and
// delegates. Kept (rather than removed) because both callers end up with a
// one-line diff each — review reads "the writer changed", nothing beyond that.
//
// The previous body preserved the MODE and silently lost the OWNER.
func writeJSONAtomic(path string, cfg map[string]interface{}, ref string) error {
	out, err := json.MarshalIndent(cfg, "", "    ")
	if err != nil {
		return err
	}
	return escreveAtomico(path, out, ref)
}
