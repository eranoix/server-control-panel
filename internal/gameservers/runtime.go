package gameservers

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type RuntimeOpt struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Label string `json:"label"`
	Help  string `json:"help"`
	Kind  string `json:"kind"`
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
	_ = writeAtomic(path+".bak", b, path)
	if err := writeAtomic(path, []byte(txt), path); err != nil {
		return err
	}

	if m.dc == nil {
		return fmt.Errorf("saved, but docker is unavailable to recreate the container")
	}
	if _, err := m.dc.ComposeAction(filepath.Base(s.Root), s.Root, "up"); err != nil {
		return fmt.Errorf("saved, but recreating the container failed: %w", err)
	}
	return nil
}

func setComposeEnv(txt, key, val string) string {
	re := regexp.MustCompile(`(?m)^(\s*)` + regexp.QuoteMeta(key) + `:\s*"?[^"\n]*"?\s*$`)
	if re.MatchString(txt) {
		if val == "" {
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
