package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"server-control-panel/internal/config"
	"server-control-panel/internal/scope"
	"server-control-panel/internal/secrets"
)

// cmdSecrets dispatches `vpsmctl secrets <sub>`. Operates on the on-disk vault
// directly (DataDir/secrets.vault) with the same passphrase the server uses
// (cfg.JWTSecret), so it works for recovery and for bulk provisioning the
// "used across the whole VPS" credential store.
//
// IMPORTANT: changes written here land on disk. A running server keeps its own
// in-memory copy of the vault opened at boot and will NOT see new entries until
// it is restarted. The caller (or `agentctl deploy`) must restart the service
// for imports to show up in the live UI.
func cmdSecrets(args []string) error {
	if len(args) < 1 {
		secretsUsage()
		return fmt.Errorf("secrets: missing subcommand")
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "list":
		return cmdSecretsList(rest)
	case "get":
		return cmdSecretsGet(rest)
	case "set":
		return cmdSecretsSet(rest)
	case "tag":
		return cmdSecretsTag(rest)
	case "delete", "rm":
		return cmdSecretsDelete(rest)
	case "export":
		return cmdSecretsExport(rest)
	case "import-env":
		return cmdSecretsImportEnv(rest)
	case "import":
		return cmdSecretsImport(rest)
	case "-h", "--help", "help":
		secretsUsage()
		return nil
	default:
		secretsUsage()
		return fmt.Errorf("secrets: unknown subcommand %q", sub)
	}
}

func secretsUsage() {
	fmt.Fprint(os.Stderr, `vpsmctl secrets — manage the encrypted credential vault

Usage:
  vpsmctl secrets list [--user U] [--group G]
        list keys (with group/type); does NOT show values
  vpsmctl secrets get [--user U] <key>
        print the VALUE of <key> on stdout (to inject it into another project)
  vpsmctl secrets set [--user U] [--group G] [--type T] [--notes N] <key> [value]
        store <key>; the value comes from the arg or from stdin (if omitted)
  vpsmctl secrets tag [--user U] [--group G] [--type T] [--notes N] <key...>
        (re)group/label EXISTING keys — updates only the metadata, does NOT touch the value
  vpsmctl secrets delete [--user U] <key>
        remove <key> (value + metadata)
  vpsmctl secrets export [--user U] [--group G] [--format env|json]
        dump credentials (env: KEY=VALUE; json: {key:value})
  vpsmctl secrets import-env [--user U] --group G [--type T] [--dry-run] [--prefix P] <file.env>
        import every KEY=VALUE from a .env file into group G
  vpsmctl secrets import [--user U] <manifest.json>
        import [{key,value,group,type,notes}] from a JSON manifest

Default --user = the config's primary. Values are never logged (only names).
`)
}

// vaultFor opens the vault and binds it to the requested (or primary) user.
func vaultFor(userFlag string) (*scope.UserVault, string, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, "", fmt.Errorf("load config: %w", err)
	}
	if cfg.JWTSecret == "" {
		return nil, "", fmt.Errorf("config.JWTSecret empty — refusing to open vault")
	}
	username := userFlag
	if username == "" {
		username = cfg.Primary
	}
	if username == "" {
		return nil, "", fmt.Errorf("no --user given and config has no primary")
	}
	u, err := scope.New(username)
	if err != nil {
		return nil, "", fmt.Errorf("invalid user %q: %w", username, err)
	}
	st, err := secrets.Open(filepath.Join(cfg.DataDir, "secrets.vault"), cfg.JWTSecret)
	if err != nil {
		return nil, "", fmt.Errorf("open vault: %w", err)
	}
	return scope.NewUserVault(st, u), username, nil
}

func cmdSecretsList(args []string) error {
	fs := flag.NewFlagSet("secrets list", flag.ContinueOnError)
	user := fs.String("user", "", "vault owner (default: primary)")
	group := fs.String("group", "", "filter by group")
	if err := fs.Parse(args); err != nil {
		return err
	}
	uv, who, err := vaultFor(*user)
	if err != nil {
		return err
	}
	entries := uv.ListEntries()
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Group != entries[j].Group {
			return entries[i].Group < entries[j].Group
		}
		return entries[i].Key < entries[j].Key
	})
	n := 0
	fmt.Printf("# vault of %s\n", who)
	for _, e := range entries {
		if *group != "" && e.Group != *group {
			continue
		}
		g := e.Group
		if g == "" {
			g = "(no group)"
		}
		t := e.Type
		if t == "" {
			t = "-"
		}
		fmt.Printf("  [%s] %-30s type=%-10s %s\n", g, e.Key, t, e.Notes)
		n++
	}
	fmt.Printf("# %d credentials\n", n)
	return nil
}

func cmdSecretsGet(args []string) error {
	fs := flag.NewFlagSet("secrets get", flag.ContinueOnError)
	user := fs.String("user", "", "vault owner (default: primary)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: secrets get [--user U] <key>")
	}
	uv, _, err := vaultFor(*user)
	if err != nil {
		return err
	}
	v, ok := uv.Get(fs.Arg(0))
	if !ok {
		return fmt.Errorf("not found: %s", fs.Arg(0))
	}
	fmt.Print(v) // raw, no newline — composes with $(...) injection
	return nil
}

func cmdSecretsSet(args []string) error {
	fs := flag.NewFlagSet("secrets set", flag.ContinueOnError)
	user := fs.String("user", "", "vault owner (default: primary)")
	group := fs.String("group", "", "group / project")
	typ := fs.String("type", "", "type (password|token|api-key|cert|url|ssh-key|other)")
	notes := fs.String("notes", "", "free-text notes")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: secrets set [flags] <key> [value]")
	}
	key := fs.Arg(0)
	var value string
	if fs.NArg() >= 2 {
		value = fs.Arg(1)
	} else {
		// read value from stdin (avoids leaking it into shell history)
		b, err := readAllStdin()
		if err != nil {
			return err
		}
		value = strings.TrimRight(b, "\n")
	}
	uv, _, err := vaultFor(*user)
	if err != nil {
		return err
	}
	if err := uv.SetWithMeta(key, value, scope.EntryMeta{Group: *group, Type: *typ, Notes: *notes}); err != nil {
		return err
	}
	fmt.Printf("ok: %s stored (%d bytes)\n", key, len(value))
	return nil
}

// cmdSecretsTag (re)groups/tags existing keys by updating ONLY their metadata
// (group/type/notes) — the stored value is never read out nor rewritten. Used
// to file the daemon's own ungrouped keys (waha_*/jira_*) into a group.
func cmdSecretsTag(args []string) error {
	fs := flag.NewFlagSet("secrets tag", flag.ContinueOnError)
	user := fs.String("user", "", "vault owner (default: primary)")
	group := fs.String("group", "", "group / project")
	typ := fs.String("type", "", "type (password|token|api-key|cert|url|ssh-key|other)")
	notes := fs.String("notes", "", "free-text notes")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: secrets tag [flags] <key...>")
	}
	uv, who, err := vaultFor(*user)
	if err != nil {
		return err
	}
	n := 0
	for _, key := range fs.Args() {
		if _, ok := uv.Get(key); !ok {
			fmt.Printf("  skip %s (does not exist)\n", key)
			continue
		}
		if err := uv.SetMeta(key, scope.EntryMeta{Group: *group, Type: *typ, Notes: *notes}); err != nil {
			return fmt.Errorf("tag %s: %w", key, err)
		}
		n++
	}
	fmt.Printf("ok: %d keys tagged group=%q (vault of %s)\n", n, *group, who)
	return nil
}

func cmdSecretsDelete(args []string) error {
	fs := flag.NewFlagSet("secrets delete", flag.ContinueOnError)
	user := fs.String("user", "", "vault owner (default: primary)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: secrets delete [--user U] <key>")
	}
	uv, _, err := vaultFor(*user)
	if err != nil {
		return err
	}
	if err := uv.Delete(fs.Arg(0)); err != nil {
		return err
	}
	fmt.Printf("ok: %s removed\n", fs.Arg(0))
	return nil
}

func cmdSecretsExport(args []string) error {
	fs := flag.NewFlagSet("secrets export", flag.ContinueOnError)
	user := fs.String("user", "", "vault owner (default: primary)")
	group := fs.String("group", "", "filter by group")
	format := fs.String("format", "env", "env|json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	uv, _, err := vaultFor(*user)
	if err != nil {
		return err
	}
	entries := uv.ListEntries()
	out := map[string]string{}
	for _, e := range entries {
		if *group != "" && e.Group != *group {
			continue
		}
		if v, ok := uv.Get(e.Key); ok {
			out[e.Key] = v
		}
	}
	switch *format {
	case "json":
		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(b))
	case "env":
		keys := make([]string, 0, len(out))
		for k := range out {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Printf("%s=%s\n", k, out[k])
		}
	default:
		return fmt.Errorf("invalid format: %s (use env|json)", *format)
	}
	return nil
}

// cmdSecretsImportEnv reads a .env-style file and imports each KEY=VALUE into
// the vault under the given group. The values are read by THIS process from
// the source file and written straight to the vault — they are never printed.
func cmdSecretsImportEnv(args []string) error {
	fs := flag.NewFlagSet("secrets import-env", flag.ContinueOnError)
	user := fs.String("user", "", "vault owner (default: primary)")
	group := fs.String("group", "", "group / project (required)")
	typ := fs.String("type", "", "type stamped on every imported key")
	prefix := fs.String("prefix", "", "prefix added to each imported key name")
	only := fs.String("only", "", "CSV allowlist: import ONLY these key names (curated)")
	dryRun := fs.Bool("dry-run", false, "list keys that WOULD be imported, write nothing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: secrets import-env [flags] <file.env>")
	}
	if *group == "" && !*dryRun {
		return fmt.Errorf("--group is required (use --dry-run to list only)")
	}
	path := fs.Arg(0)
	pairs, err := parseEnvFile(path)
	if err != nil {
		return err
	}
	if *only != "" {
		allow := map[string]bool{}
		for _, k := range strings.Split(*only, ",") {
			if k = strings.TrimSpace(k); k != "" {
				allow[k] = true
			}
		}
		filtered := pairs[:0]
		for _, p := range pairs {
			if allow[p.key] {
				filtered = append(filtered, p)
			}
		}
		pairs = filtered
	}
	if len(pairs) == 0 {
		fmt.Printf("# %s: no KEY=VALUE pair found\n", path)
		return nil
	}
	if *dryRun {
		fmt.Printf("# %s → group %q: %d keys (dry-run, nothing written)\n", path, *group, len(pairs))
		for _, p := range pairs {
			fmt.Printf("  %s%s\n", *prefix, p.key)
		}
		return nil
	}
	uv, who, err := vaultFor(*user)
	if err != nil {
		return err
	}
	n := 0
	for _, p := range pairs {
		key := *prefix + p.key
		if err := uv.SetWithMeta(key, p.value, scope.EntryMeta{Group: *group, Type: *typ, Notes: "import-env: " + filepath.Base(path)}); err != nil {
			return fmt.Errorf("set %s: %w", key, err)
		}
		n++
	}
	fmt.Printf("ok: %d keys from %s imported into group %q (vault of %s)\n", n, path, *group, who)
	return nil
}

// manifestEntry is one credential in a JSON import manifest.
type manifestEntry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Group string `json:"group"`
	Type  string `json:"type"`
	Notes string `json:"notes"`
}

func cmdSecretsImport(args []string) error {
	fs := flag.NewFlagSet("secrets import", flag.ContinueOnError)
	user := fs.String("user", "", "vault owner (default: primary)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: secrets import [--user U] <manifest.json>")
	}
	raw, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}
	var items []manifestEntry
	if err := json.Unmarshal(raw, &items); err != nil {
		return fmt.Errorf("parse manifest (expects an array of {key,value,group,type,notes}): %w", err)
	}
	uv, who, err := vaultFor(*user)
	if err != nil {
		return err
	}
	n := 0
	for _, it := range items {
		if it.Key == "" {
			continue
		}
		if err := uv.SetWithMeta(it.Key, it.Value, scope.EntryMeta{Group: it.Group, Type: it.Type, Notes: it.Notes}); err != nil {
			return fmt.Errorf("set %s: %w", it.Key, err)
		}
		n++
	}
	fmt.Printf("ok: %d credentials imported (vault of %s)\n", n, who)
	return nil
}

// --- helpers ---

type envPair struct{ key, value string }

// parseEnvFile reads KEY=VALUE lines. Skips blanks, comments (#), and the
// optional leading "export ". Strips matching single/double quotes around the
// value. Anything without an "=" is ignored.
func parseEnvFile(path string) ([]envPair, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	var out []envPair
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024) // tolerate long values (PEM, etc.)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		if key == "" {
			continue
		}
		out = append(out, envPair{key: key, value: val})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func readAllStdin() (string, error) {
	var sb strings.Builder
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	first := true
	for sc.Scan() {
		if !first {
			sb.WriteByte('\n')
		}
		sb.WriteString(sc.Text())
		first = false
	}
	return sb.String(), sc.Err()
}
