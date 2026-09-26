package gameservers

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// The server's INFRA options, which live in docker-compose.yml and not in the
// game config: scheduled restart, auto-update time and how many backups to
// keep.
//
// Editing the compose requires recreating the container for it to take effect
// (env vars are read at creation time), which is why Apply runs `up -d`.

// RuntimeOpt describes one editable env var of the compose.
type RuntimeOpt struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Label string `json:"label"`
	Help  string `json:"help"`
	Kind  string `json:"kind"` // cron | int
}

var runtimeOpts = []RuntimeOpt{
	{Key: "RESTART_CRON", Kind: "cron", Label: "Scheduled restart",
		Help: "Cron for when to restart on its own. E.g. 0 6 * * * = every day at 6am. Empty = never."},
	{Key: "UPDATE_CRON", Kind: "cron", Label: "Check for updates",
		Help: "Cron for when to look for a game update. E.g. 0 5 * * * = every day at 5am."},
	{Key: "BACKUP_CRON", Kind: "cron", Label: "Automatic backup",
		Help: "Cron for when to take a backup. E.g. 0 */6 * * * = every 6 hours."},
	{Key: "BACKUP_MAX_COUNT", Kind: "int", Label: "Backups kept",
		Help: "How many automatic backups to keep. The oldest ones are deleted."},
}

func composePath(s Server) string { return filepath.Join(s.Root, "docker-compose.yml") }

// Runtime reads the current values of the compose env vars.
func (m *Manager) Runtime(s Server) ([]RuntimeOpt, error) {
	b, err := os.ReadFile(composePath(s))
	if err != nil {
		return nil, fmt.Errorf("could not find this server's docker-compose.yml")
	}
	txt := string(b)
	out := make([]RuntimeOpt, 0, len(runtimeOpts))
	for _, o := range runtimeOpts {
		o.Value = readComposeEnv(txt, o.Key)
		out = append(out, o)
	}
	return out, nil
}

func readComposeEnv(txt, key string) string {
	re := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(key) + `:\s*"?([^"\n]*)"?\s*$`)
	if mm := re.FindStringSubmatch(txt); len(mm) == 2 {
		return strings.TrimSpace(mm[1])
	}
	return ""
}

// cronOK validates 5 fields. It does not try to cover the whole dialect — only
// to block what would clearly break the container's scheduler.
var cronField = regexp.MustCompile(`^[\d*/,\-]+$`)

func cronOK(v string) error {
	f := strings.Fields(v)
	if len(f) != 5 {
		return fmt.Errorf("cron needs 5 fields (min hour day month weekday), e.g.: 0 6 * * *")
	}
	for _, x := range f {
		if !cronField.MatchString(x) {
			return fmt.Errorf("invalid cron field: %q", x)
		}
	}
	return nil
}

// SetRuntime writes the env vars into the compose and recreates the container to apply them.
func (m *Manager) SetRuntime(s Server, patch map[string]string) error {
	known := map[string]RuntimeOpt{}
	for _, o := range runtimeOpts {
		known[o.Key] = o
	}
	for k, v := range patch {
		o, ok := known[k]
		if !ok {
			return fmt.Errorf("option not editable: %s", k)
		}
		v = strings.TrimSpace(v)
		switch o.Kind {
		case "cron":
			if v != "" {
				if err := cronOK(v); err != nil {
					return fmt.Errorf("%s: %w", o.Label, err)
				}
			}
		case "int":
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > 500 {
				return fmt.Errorf("%s: provide a number from 1 to 500", o.Label)
			}
		}
		patch[k] = v
	}

	path := composePath(s)
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	txt := string(b)
	for k, v := range patch {
		txt = setComposeEnv(txt, k, v)
	}
	// Back up the compose before writing: if something goes wrong there is a way
	// back. The .bak inherits owner and mode from the ORIGINAL (`ref` = path),
	// otherwise it is born root:root beside a 4711 compose — the same class of
	// defect, quieter, because nobody looks at a backup's owner until they need it.
	_ = escreveAtomico(path+".bak", b, path)
	if err := escreveAtomico(path, []byte(txt), path); err != nil {
		return err
	}

	// The env vars only take effect when the container is recreated.
	if m.dc == nil {
		return fmt.Errorf("saved, but docker is unavailable to recreate the container")
	}
	if _, err := m.dc.ComposeAction(filepath.Base(s.Root), s.Root, "up"); err != nil {
		return fmt.Errorf("saved, but recreating the container failed: %w", err)
	}
	return nil
}

// setComposeEnv replaces the value of an existing env var, or inserts it right
// below the `environment:` block when it does not exist yet.
func setComposeEnv(txt, key, val string) string {
	re := regexp.MustCompile(`(?m)^(\s*)` + regexp.QuoteMeta(key) + `:\s*"?[^"\n]*"?\s*$`)
	if re.MatchString(txt) {
		if val == "" {
			// Empty = drop the line (the scheduler treats absence as off).
			return regexp.MustCompile(`(?m)^\s*`+regexp.QuoteMeta(key)+`:\s*"?[^"\n]*"?\s*\n`).
				ReplaceAllString(txt, "")
		}
		return re.ReplaceAllString(txt, `${1}`+key+`: "`+val+`"`)
	}
	if val == "" {
		return txt
	}
	envRe := regexp.MustCompile(`(?m)^(\s*)environment:\s*$`)
	loc := envRe.FindStringSubmatchIndex(txt)
	if loc == nil {
		return txt
	}
	indent := txt[loc[2]:loc[3]] + "  "
	insertAt := loc[1]
	return txt[:insertAt] + "\n" + indent + key + `: "` + val + `"` + txt[insertAt:]
}
