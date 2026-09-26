// jira.go — vpsmctl jira-seed: write Atlassian Cloud credentials into a
// user's vault from the CLI. Useful when you already have an API token
// from a sister project (e.g. GitHub secret) and don't want to re-type
// it through the web UI.
//
//	vpsmctl jira-seed --user=sam \
//	    --site=example.atlassian.net \
//	    --email=sam@northwind.example \
//	    --token=ATATT... \
//	    --project=TTW \
//	    --board-jql='project = TTW AND statusCategory != Done ORDER BY rank' \
//	    --board-columns='[{"label":"To Do","status_names":["To Do"]}, ...]'
//
// Any flag left empty leaves the existing vault key untouched (so you
// can rotate just the token without re-passing the email).
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"server-control-panel/internal/config"
	"server-control-panel/internal/scope"
	"server-control-panel/internal/secrets"
)

func cmdJiraSeed(args []string) error {
	fs := flag.NewFlagSet("jira-seed", flag.ContinueOnError)
	user := fs.String("user", "", "target user (required)")
	site := fs.String("site", "", "Atlassian site (e.g. example.atlassian.net or just 'northwind')")
	email := fs.String("email", "", "user email")
	token := fs.String("token", "", "API token (https://id.atlassian.com/manage-profile/security/api-tokens)")
	project := fs.String("project", "", "default project key (e.g. TTW)")
	jql := fs.String("board-jql", "", "default board JQL")
	cols := fs.String("board-columns", "", "kanban columns as JSON array")
	show := fs.Bool("show", false, "after writing, print current config (token masked)")
	wipe := fs.Bool("wipe", false, "delete all jira_* keys for the user instead of writing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *user == "" {
		fs.Usage()
		return fmt.Errorf("--user required")
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if cfg.JWTSecret == "" {
		return fmt.Errorf("config.JWTSecret empty — refusing to open vault")
	}
	vaultPath := filepath.Join(cfg.DataDir, "secrets.vault")
	vault, err := secrets.Open(vaultPath, cfg.JWTSecret)
	if err != nil {
		return fmt.Errorf("open vault: %w", err)
	}
	u, err := scope.New(*user)
	if err != nil {
		return fmt.Errorf("invalid user %q: %w", *user, err)
	}
	uv := scope.NewUserVault(vault, u)

	if *wipe {
		for _, k := range []string{"jira_site", "jira_email", "jira_token", "jira_project", "jira_board_jql", "jira_board_columns"} {
			if err := uv.Set(k, ""); err != nil {
				return fmt.Errorf("wipe %s: %w", k, err)
			}
		}
		fmt.Fprintf(os.Stderr, "wiped jira_* keys for %s\n", *user)
		return nil
	}

	setIf := func(key, val string) error {
		v := strings.TrimSpace(val)
		if v == "" {
			return nil
		}
		return uv.Set(key, v)
	}
	for _, pair := range []struct{ k, v string }{
		{"jira_site", *site},
		{"jira_email", *email},
		{"jira_token", *token},
		{"jira_project", *project},
		{"jira_board_jql", *jql},
		{"jira_board_columns", *cols},
	} {
		if err := setIf(pair.k, pair.v); err != nil {
			return fmt.Errorf("vault.set %s: %w", pair.k, err)
		}
	}
	fmt.Fprintf(os.Stderr, "wrote jira_* keys for %s (skipped empty flags)\n", *user)

	if *show {
		fmt.Println("--- current ---")
		for _, k := range []string{"jira_site", "jira_email", "jira_project", "jira_board_jql", "jira_board_columns"} {
			v, _ := uv.Get(k)
			fmt.Printf("  %-22s %s\n", k+":", v)
		}
		if v, _ := uv.Get("jira_token"); v != "" {
			fmt.Printf("  %-22s %s\n", "jira_token:", maskToken(v))
		} else {
			fmt.Printf("  %-22s (none)\n", "jira_token:")
		}
	}
	return nil
}

func maskToken(t string) string {
	if len(t) <= 8 {
		return strings.Repeat("*", len(t))
	}
	return t[:4] + strings.Repeat("*", len(t)-8) + t[len(t)-4:]
}
