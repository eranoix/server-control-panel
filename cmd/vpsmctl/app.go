// app.go — vpsmctl app-*: management of the Heroku-style PaaS. The
// post-receive hook of each bare repo calls `vpsmctl app-deploy --name <n> --hook`,
// which runs the core deploy.Deploy and streams the output back to `git push`.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"server-control-panel/internal/config"
	"server-control-panel/internal/deploy"
)

func openDeployStore() (*deploy.Store, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	if cfg.DataDir == "" {
		return nil, fmt.Errorf("config.DataDir empty")
	}
	// The SINGLE point where vpsmctl touches the deploy registry, and therefore
	// the point where the refusal belongs. This binary NEVER migrates apps.json
	// (the vps-manager boot is what migrates it); faced with a shape it does not
	// write, it fails closed BEFORE opening the store. The hot path here is the
	// post-receive hook: better a rejected `git push` with a message that names the
	// binary than a push that "works" and rewrites the envelope back to v1.
	if err := deploy.GuardCLI(cfg.DataDir); err != nil {
		return nil, err
	}
	return deploy.Open(cfg.DataDir), nil
}

func cmdAppCreate(args []string) error {
	fs := flag.NewFlagSet("app-create", flag.ContinueOnError)
	name := fs.String("name", "", "app slug [a-z0-9-] (required)")
	port := fs.Int("port", 0, "host port the app listens on ($PORT in .env)")
	domain := fs.String("domain", "", "nginx server_name (optional)")
	branch := fs.String("branch", "main", "production branch")
	compose := fs.String("compose-file", "docker-compose.yml", "path to the compose file inside the repo")
	auto := fs.Bool("autodeploy", true, "deploy automatically on push to the production branch")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *name == "" {
		fs.Usage()
		return fmt.Errorf("--name required")
	}
	st, err := openDeployStore()
	if err != nil {
		return err
	}
	app, err := st.Create(deploy.App{
		Name: *name, Port: *port, Domain: *domain, Branch: *branch,
		ComposeFile: *compose, Autodeploy: *auto,
	})
	if err != nil {
		return err
	}
	host, _ := os.Hostname()
	fmt.Printf("app %q created.\n\n", app.Name)
	fmt.Printf("  git remote add vpsm root@%s:%s\n", firstNonEmpty(host, "SEU_VPS"), deploy.RepoPath(app.Name))
	fmt.Printf("  git push vpsm %s\n\n", app.Branch)
	fmt.Printf("The push triggers build + docker compose up (the output shows up in your terminal).\n")
	return nil
}

func cmdAppDeploy(args []string) error {
	fs := flag.NewFlagSet("app-deploy", flag.ContinueOnError)
	name := fs.String("name", "", "app slug (required)")
	hook := fs.Bool("hook", false, "post-receive mode: reads <old> <new> <ref> from stdin")
	ref := fs.String("ref", "", "ref to deploy (default: production branch)")
	commit := fs.String("commit", "", "commit to deploy (default: HEAD of the ref)")
	preview := fs.String("preview", "", "preview env slug (empty = production)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *name == "" {
		return fmt.Errorf("--name required")
	}
	st, err := openDeployStore()
	if err != nil {
		return err
	}
	app, ok, err := st.Get(*name)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("app %q does not exist (run app-create)", *name)
	}
	branch := app.Branch
	if branch == "" {
		branch = "main"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	if *hook {
		refs := deploy.ParseHookStdin(os.Stdin)
		if len(refs) == 0 {
			return nil
		}
		var firstErr error
		for _, hr := range refs {
			if hr.Ref == "refs/heads/"+branch {
				if !app.Autodeploy {
					fmt.Printf("autodeploy off — skipped %s\n", hr.Ref)
					continue
				}
				_, e := deploy.Deploy(ctx, st, deploy.Spec{
					App: *name, Ref: hr.Ref, Commit: hr.Commit, Trigger: "push",
				}, os.Stdout)
				if e != nil && firstErr == nil {
					firstErr = e
				}
			} else {
				// Per-branch preview env: an isolated, ephemeral stack.
				slug := deploy.PreviewSlug(hr.Ref)
				if slug == "" {
					continue
				}
				if hr.Delete {
					fmt.Printf("branch %s deleted — tearing down preview %s\n",
						strings.TrimPrefix(hr.Ref, "refs/heads/"), slug)
					cur, _, _ := st.Get(*name)
					_ = deploy.TeardownPreview(ctx, st, cur, slug, os.Stdout)
					continue
				}
				cur, _, _ := st.Get(*name)
				if ok, msg := deploy.CanDeployPreview(cur, slug); !ok {
					fmt.Printf("preview %s refused: %s\n", slug, msg)
					if firstErr == nil {
						firstErr = fmt.Errorf("%s", msg)
					}
					continue
				}
				_, e := deploy.Deploy(ctx, st, deploy.Spec{
					App: *name, Ref: hr.Ref, Commit: hr.Commit, Preview: slug, Trigger: "push",
				}, os.Stdout)
				if e != nil && firstErr == nil {
					firstErr = e
				}
			}
		}
		return firstErr
	}

	_, err = deploy.Deploy(ctx, st, deploy.Spec{
		App: *name, Ref: *ref, Commit: *commit, Preview: *preview, Trigger: "ui",
	}, os.Stdout)
	return err
}

func cmdAppList(args []string) error {
	st, err := openDeployStore()
	if err != nil {
		return err
	}
	apps, err := st.List()
	if err != nil {
		return err
	}
	if len(apps) == 0 {
		fmt.Println("(no apps)")
		return nil
	}
	fmt.Printf("%-20s %-8s %-24s %-6s %s\n", "APP", "BRANCH", "DOMAIN", "PORT", "LAST DEPLOY")
	for _, a := range apps {
		last := "—"
		if d, ok := deploy.LastProdDeploy(a); ok {
			last = fmt.Sprintf("%s %s", d.Status, short12(d.Commit))
		}
		port := "—"
		if a.Port > 0 {
			port = fmt.Sprintf("%d", a.Port)
		}
		fmt.Printf("%-20s %-8s %-24s %-6s %s\n", a.Name, a.Branch, firstNonEmpty(a.Domain, "—"), port, last)
	}
	return nil
}

func cmdAppRollback(args []string) error {
	fs := flag.NewFlagSet("app-rollback", flag.ContinueOnError)
	name := fs.String("name", "", "app slug (required)")
	to := fs.String("to", "", "specific commit (default: previous deploy)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *name == "" {
		return fmt.Errorf("--name required")
	}
	st, err := openDeployStore()
	if err != nil {
		return err
	}
	app, ok, err := st.Get(*name)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("app %q does not exist", *name)
	}
	target := *to
	if target == "" {
		target = deploy.PreviousCommit(app)
	}
	if target == "" {
		return fmt.Errorf("no previous deploy to roll back to")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	_, err = deploy.Deploy(ctx, st, deploy.Spec{
		App: *name, Commit: target, Trigger: "rollback",
	}, os.Stdout)
	return err
}

func cmdAppCatalog(args []string) error {
	fmt.Printf("%-14s %-11s %-4s %s\n", "ID", "CATEGORY", "WEB", "NAME")
	for _, t := range deploy.Catalog() {
		web := "—"
		if t.Web {
			web = "yes"
		}
		fmt.Printf("%-14s %-11s %-4s %s\n", t.ID, t.Category, web, t.Name)
	}
	return nil
}

func cmdAppFromTemplate(args []string) error {
	fs := flag.NewFlagSet("app-from-template", flag.ContinueOnError)
	template := fs.String("template", "", "template id (see app-catalog) (required)")
	name := fs.String("name", "", "app slug (required)")
	port := fs.Int("port", 0, "host port (default: the template's)")
	domain := fs.String("domain", "", "nginx server_name (optional)")
	var sets multiFlag
	fs.Var(&sets, "set", "env KEY=VALUE (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *template == "" || *name == "" {
		return fmt.Errorf("--template and --name are required")
	}
	tmpl, ok := deploy.TemplateByID(*template)
	if !ok {
		return fmt.Errorf("unknown template: %s", *template)
	}
	env := map[string]string{}
	for _, kv := range sets {
		i := strings.IndexByte(kv, '=')
		if i <= 0 {
			return fmt.Errorf("invalid --set %q", kv)
		}
		env[kv[:i]] = kv[i+1:]
	}
	st, err := openDeployStore()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	_, _, err = st.CreateFromTemplate(ctx, tmpl, deploy.App{Name: *name, Port: *port, Domain: *domain}, env, os.Stdout)
	return err
}

func cmdAppEnv(args []string) error {
	fs := flag.NewFlagSet("app-env", flag.ContinueOnError)
	name := fs.String("name", "", "app slug (required)")
	preview := fs.Bool("preview", false, "operate on the preview env instead of production")
	list := fs.Bool("list", false, "list the vars")
	unset := fs.String("unset", "", "remove one key")
	var sets multiFlag
	fs.Var(&sets, "set", "set KEY=VALUE (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *name == "" {
		return fmt.Errorf("--name required")
	}
	st, err := openDeployStore()
	if err != nil {
		return err
	}
	app, ok, err := st.Get(*name)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("app %q does not exist", *name)
	}
	target := app.Env
	if *preview {
		if app.PreviewEnv == nil {
			app.PreviewEnv = map[string]string{}
		}
		target = app.PreviewEnv
	} else if target == nil {
		target = map[string]string{}
		app.Env = target
	}
	dirty := false
	for _, kv := range sets {
		i := strings.IndexByte(kv, '=')
		if i <= 0 {
			return fmt.Errorf("invalid --set %q (use KEY=VALUE)", kv)
		}
		target[kv[:i]] = kv[i+1:]
		dirty = true
	}
	if *unset != "" {
		delete(target, *unset)
		dirty = true
	}
	if dirty {
		if err := st.Save(app); err != nil {
			return err
		}
		fmt.Println("env updated (effective on the next deploy)")
	}
	if *list || !dirty {
		scope := "production"
		if *preview {
			scope = "preview"
		}
		fmt.Printf("# env (%s) of %s\n", scope, *name)
		for k, v := range target {
			fmt.Printf("%s=%s\n", k, v)
		}
	}
	return nil
}

func cmdAppDestroy(args []string) error {
	fs := flag.NewFlagSet("app-destroy", flag.ContinueOnError)
	name := fs.String("name", "", "app slug (required)")
	yes := fs.Bool("yes", false, "confirm (required — removes containers/repo/data)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *name == "" {
		return fmt.Errorf("--name required")
	}
	if !*yes {
		return fmt.Errorf("pass --yes to confirm (tears down the stack, deletes repo and work-tree)")
	}
	st, err := openDeployStore()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := st.Destroy(ctx, *name, os.Stdout); err != nil {
		return err
	}
	fmt.Printf("app %q destroyed\n", *name)
	return nil
}

func cmdAppPreviewReap(args []string) error {
	st, err := openDeployStore()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	n, err := deploy.ReapPreviews(ctx, st, os.Stdout)
	if err != nil {
		return err
	}
	fmt.Printf("previews torn down: %d\n", n)
	return nil
}

func cmdAppPreviewTeardown(args []string) error {
	fs := flag.NewFlagSet("app-preview-teardown", flag.ContinueOnError)
	name := fs.String("name", "", "app slug (required)")
	slug := fs.String("slug", "", "preview slug (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *name == "" || *slug == "" {
		return fmt.Errorf("--name and --slug are required")
	}
	st, err := openDeployStore()
	if err != nil {
		return err
	}
	app, ok, err := st.Get(*name)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("app %q does not exist", *name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := deploy.TeardownPreview(ctx, st, app, *slug, os.Stdout); err != nil {
		return err
	}
	fmt.Printf("preview %s/%s torn down\n", *name, *slug)
	return nil
}

// multiFlag accumulates repeatable flags (--set a=1 --set b=2).
type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}

func short12(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
