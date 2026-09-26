// jira_create.go — vpsmctl jira-create: creates a Jira issue using the user's
// vault credentials (the same path as jira-comment). vps-manager itself does
// the creating; no session JWT.
//
//	vpsmctl jira-create --user=sam --type=Task --summary="..." --desc-file=/tmp/d.txt
//	  [--project=PROJ] [--labels=a,b] [--parent=PROJ-10]
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

func cmdJiraCreate(args []string) error {
	fs := flag.NewFlagSet("jira-create", flag.ContinueOnError)
	user := fs.String("user", "", "vault owner whose Jira credentials to use (required)")
	project := fs.String("project", "", "project key (default: jira_project from the vault)")
	itype := fs.String("type", "Task", "issue type (Task, Bug, Story, Epic)")
	summary := fs.String("summary", "", "issue title (required)")
	desc := fs.String("desc", "", "description (or use --desc-file)")
	descFile := fs.String("desc-file", "", "read the description from this file")
	labels := fs.String("labels", "", "comma-separated labels")
	parent := fs.String("parent", "", "parent issue (epic-link/subtask)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *user == "" || *summary == "" {
		fs.Usage()
		return fmt.Errorf("--user and --summary are required")
	}
	description := *desc
	if *descFile != "" {
		b, err := os.ReadFile(*descFile)
		if err != nil {
			return fmt.Errorf("read desc-file: %w", err)
		}
		description = string(b)
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
	proj := *project
	if proj == "" {
		proj, _ = uv.Get("jira_project")
	}
	if proj == "" {
		return fmt.Errorf("--project required (vault has no jira_project)")
	}
	cli, err := jira.New(site, email, token)
	if err != nil {
		return fmt.Errorf("jira client: %w", err)
	}

	req := jira.CreateIssueRequest{
		ProjectKey:  proj,
		IssueType:   *itype,
		Summary:     *summary,
		Description: description,
		ParentKey:   *parent,
	}
	if strings.TrimSpace(*labels) != "" {
		for _, l := range strings.Split(*labels, ",") {
			if l = strings.TrimSpace(l); l != "" {
				req.Labels = append(req.Labels, l)
			}
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	got, err := cli.CreateIssue(ctx, req)
	if err != nil {
		return fmt.Errorf("create issue: %w", err)
	}
	fmt.Println(got.Key) // stdout = the key, for a script to capture
	fmt.Fprintf(os.Stderr, "created %s (%s): %s\n", got.Key, *itype, *summary)
	return nil
}
