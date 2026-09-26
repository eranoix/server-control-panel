// jira_comment.go — vpsmctl jira-comment: posts a comment on a Jira issue
// using the user's vault credentials — the SAME path the web backend takes
// (jiraClientForOwner). It is vps-manager itself doing the post; no session
// JWT and no minted token needed.
//
//	vpsmctl jira-comment --user=sam --key=PROJ-68 --body-file=/tmp/block.txt
//	vpsmctl jira-comment --user=sam --key=PROJ-69 --body="texto curto"
//
// Every credential flag comes from the vault (jira_site/jira_email/jira_token),
// seeded beforehand via `vpsmctl jira-seed`. The body is plain text; the client
// converts it to ADF (textToADF) just like the /api/jira/issue/{key}/comment
// endpoint does.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"server-control-panel/internal/config"
	"server-control-panel/internal/jira"
	"server-control-panel/internal/scope"
	"server-control-panel/internal/secrets"
)

func cmdJiraComment(args []string) error {
	fs := flag.NewFlagSet("jira-comment", flag.ContinueOnError)
	user := fs.String("user", "", "vault owner whose Jira credentials to use (required)")
	key := fs.String("key", "", "issue key, e.g. PROJ-68 (required)")
	body := fs.String("body", "", "comment text (or use --body-file)")
	bodyFile := fs.String("body-file", "", "read the comment text from this file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *user == "" || *key == "" {
		fs.Usage()
		return fmt.Errorf("--user and --key are required")
	}

	text := *body
	if *bodyFile != "" {
		b, err := os.ReadFile(*bodyFile)
		if err != nil {
			return fmt.Errorf("read body-file: %w", err)
		}
		text = string(b)
	}
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("empty comment (use --body or --body-file)")
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if cfg.JWTSecret == "" {
		return fmt.Errorf("config.JWTSecret empty — refusing to open the vault")
	}
	vault, err := secrets.Open(filepath.Join(cfg.DataDir, "secrets.vault"), cfg.JWTSecret)
	if err != nil {
		return fmt.Errorf("open vault: %w", err)
	}
	u, err := scope.New(*user)
	if err != nil {
		return fmt.Errorf("invalid user %q: %w", *user, err)
	}
	uv := scope.NewUserVault(vault, u)
	site, _ := uv.Get("jira_site")
	email, _ := uv.Get("jira_email")
	token, _ := uv.Get("jira_token")
	if site == "" || email == "" || token == "" {
		return fmt.Errorf("Jira not configured for %s (run jira-seed)", *user)
	}
	cli, err := jira.New(site, email, token)
	if err != nil {
		return fmt.Errorf("jira client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := cli.AddComment(ctx, *key, text)
	if err != nil {
		return fmt.Errorf("comment on %s: %w", *key, err)
	}
	fmt.Fprintf(os.Stderr, "comment posted on %s (id=%s, %d chars)\n", *key, c.ID, len(text))
	return nil
}
