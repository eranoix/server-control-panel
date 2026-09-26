// handlers_scheduler_catalog.go — server-driven catalogue of schedulable
// task kinds, plus the per-kind authorization helper that closes
// the scheduler's privilege-escalation bypass.
//
// Why a catalogue: the old UI hard-coded its task dropdown (6 options), which
// drifted from the registered runners (jira_ai_analysis was missing) and — far
// worse — offered primary-only kinds to every user, since the scheduler's HTTP
// handlers never called Runner.AuthorizedFor the way the queue's enqueue path
// does (handlers_queue.go:75). GET /api/scheduler/catalog now derives the list
// from the live runner registry, filtered by what THIS user may actually run,
// so the UI can never present an option the server will reject. This mirrors
// the metrics catalogue pattern (/api/metrics/catalog).
package api

import (
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"

	"server-control-panel/internal/auth"
	ptysvc "server-control-panel/internal/pty"
	"server-control-panel/internal/sysextra"
)

// schedOption / schedOptGroup feed the dynamic <select> widgets (type=select).
type schedOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}
type schedOptGroup struct {
	Label   string        `json:"label"`
	Options []schedOption `json:"options"`
}

// handleSchedulerOptions serves GET /api/scheduler/options?source=... — the
// dynamic option lists that power the smart dropdowns (systemd services, docker
// containers, compose projects, local images), so the operator picks from a
// grouped list instead of typing. Primary-only; reuses the same data the docker
// and system pages already expose. allow_custom=true means the UI also offers a
// "type manually" fallback (e.g. an image not yet pulled).
func (r *Router) handleSchedulerOptions(w http.ResponseWriter, req *http.Request) {
	source := req.URL.Query().Get("source")

	// sessoes_do_usuario is scoped to the user THEMSELVES (each account sees only its
	// own sessions), so it is open to any authenticated user — unlike the other
	// sources (systemd/docker/databases/…), which list host-wide resources and
	// therefore require primary. Handled before the primary gate.
	if source == "sessoes_do_usuario" {
		user := auth.UserFrom(req)
		if user == "" {
			writeErr(w, 401, "unauthorized")
			return
		}
		// Always-present option: "Todas" saves EACH active session individually
		// (one backup per session) at fire time. It comes first so it is the
		// default "back up everything" path.
		groups := []schedOptGroup{{
			Label:   "Atalho",
			Options: []schedOption{{Value: "all", Label: "All active sessions (each saved individually)"}},
		}}
		if r.sessionOwn != nil {
			if sessions, err := ptysvc.SessionListForUser(user, r.isPrimary(user), r.sessionOwn); err == nil {
				var opts []schedOption
				for _, s := range sessions {
					name, _ := s["name"].(string)
					if name == "" {
						continue
					}
					opts = append(opts, schedOption{Value: name, Label: name})
				}
				sort.Slice(opts, func(i, j int) bool { return opts[i].Value < opts[j].Value })
				if len(opts) > 0 {
					groups = append(groups, schedOptGroup{Label: "One specific session", Options: opts})
				}
			}
		}
		writeJSON(w, map[string]any{"groups": groups, "allow_custom": true})
		return
	}

	if _, ok := r.mustPrimary(w, req); !ok {
		return
	}
	groups := []schedOptGroup{}
	switch source {
	case "systemd_units":
		units, err := sysextra.ListUnits()
		if err == nil {
			var active, failed, inactive []schedOption
			for _, u := range units {
				if u.Name == "" {
					continue
				}
				opt := schedOption{Value: u.Name, Label: u.Name}
				switch {
				case u.Sub == "failed" || strings.Contains(u.Active, "fail"):
					failed = append(failed, opt)
				case u.Active == "active":
					active = append(active, opt)
				default:
					inactive = append(inactive, opt)
				}
			}
			if len(active) > 0 {
				groups = append(groups, schedOptGroup{Label: "Ativos", Options: active})
			}
			if len(failed) > 0 {
				groups = append(groups, schedOptGroup{Label: "Falhando", Options: failed})
			}
			if len(inactive) > 0 {
				groups = append(groups, schedOptGroup{Label: "Inativos", Options: inactive})
			}
		}
	case "docker_containers":
		if r.docker != nil {
			if list, err := r.docker.ListContainers(req.Context()); err == nil {
				var running, other []schedOption
				for _, c := range list {
					name := ""
					if len(c.Names) > 0 {
						name = strings.TrimPrefix(c.Names[0], "/")
					}
					if name == "" {
						continue
					}
					opt := schedOption{Value: name, Label: name + " · " + c.Image}
					if c.State == "running" {
						running = append(running, opt)
					} else {
						other = append(other, opt)
					}
				}
				if len(running) > 0 {
					groups = append(groups, schedOptGroup{Label: "Rodando", Options: running})
				}
				if len(other) > 0 {
					groups = append(groups, schedOptGroup{Label: "Parados", Options: other})
				}
			}
		}
	case "compose_projects":
		if r.docker != nil {
			if ps, err := r.docker.ListComposeProjects(req.Context()); err == nil {
				var opts []schedOption
				for _, p := range ps {
					if p.WorkingDir == "" {
						continue
					}
					label := p.Name
					if p.WorkingDir != "" {
						label = p.Name + " · " + p.WorkingDir
					}
					opts = append(opts, schedOption{Value: p.WorkingDir, Label: label})
				}
				if len(opts) > 0 {
					groups = append(groups, schedOptGroup{Label: "Compose projects", Options: opts})
				}
			}
		}
	case "docker_images":
		if r.docker != nil {
			if imgs, err := r.docker.Images(req.Context()); err == nil {
				var opts []schedOption
				for _, im := range imgs {
					for _, t := range im.RepoTags {
						if t == "" || t == "<none>:<none>" {
							continue
						}
						opts = append(opts, schedOption{Value: t, Label: t})
					}
				}
				sort.Slice(opts, func(i, j int) bool { return opts[i].Value < opts[j].Value })
				if len(opts) > 0 {
					groups = append(groups, schedOptGroup{Label: "Local images", Options: opts})
				}
			}
		}
	case "rclone_remotes":
		if _, err := exec.LookPath("rclone"); err == nil {
			if out, err := exec.CommandContext(req.Context(), "rclone", "listremotes").Output(); err == nil {
				var opts []schedOption
				for _, line := range strings.Split(string(out), "\n") {
					name := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line), ":"))
					if name != "" {
						opts = append(opts, schedOption{Value: name, Label: name})
					}
				}
				sort.Slice(opts, func(i, j int) bool { return opts[i].Value < opts[j].Value })
				if len(opts) > 0 {
					groups = append(groups, schedOptGroup{Label: "Remotes (cloud)", Options: opts})
				}
			}
		}
	case "databases":
		if out, err := exec.CommandContext(req.Context(), "sudo", "-u", "postgres", "psql", "-At", "-c", "SELECT datname FROM pg_database WHERE datistemplate=false ORDER BY datname").Output(); err == nil {
			var opts []schedOption
			for _, l := range strings.Split(string(out), "\n") {
				if l = strings.TrimSpace(l); l != "" {
					opts = append(opts, schedOption{Value: l, Label: l})
				}
			}
			if len(opts) > 0 {
				groups = append(groups, schedOptGroup{Label: "PostgreSQL", Options: opts})
			}
		}
		if out, err := exec.CommandContext(req.Context(), "mysql", "-N", "-B", "-e", "SHOW DATABASES").Output(); err == nil {
			var opts []schedOption
			for _, l := range strings.Split(string(out), "\n") {
				l = strings.TrimSpace(l)
				switch l {
				case "", "information_schema", "performance_schema", "mysql", "sys":
					continue
				}
				opts = append(opts, schedOption{Value: l, Label: l})
			}
			if len(opts) > 0 {
				groups = append(groups, schedOptGroup{Label: "MySQL", Options: opts})
			}
		}
	case "certbot_certs":
		// Lista os nomes em /etc/letsencrypt/live/* (cada dir = um certificado).
		if entries, err := os.ReadDir("/etc/letsencrypt/live"); err == nil {
			var opts []schedOption
			for _, e := range entries {
				if e.IsDir() && e.Name() != "README" {
					opts = append(opts, schedOption{Value: e.Name(), Label: e.Name()})
				}
			}
			sort.Slice(opts, func(i, j int) bool { return opts[i].Value < opts[j].Value })
			if len(opts) > 0 {
				groups = append(groups, schedOptGroup{Label: "Certificados", Options: opts})
			}
		}
	default:
		writeErr(w, 400, "unknown source: "+source)
		return
	}
	writeJSON(w, map[string]any{"groups": groups, "allow_custom": true})
}

// schedArg describes one input field a kind accepts. The UI builds its form
// from this schema (type drives the widget); the server stays authoritative —
// the runners themselves revalidate args on execution.
type schedArg struct {
	Name        string   `json:"name"`
	Label       string   `json:"label"`
	Type        string   `json:"type"`              // "enum" | "string" | "string_list" | "number" | "folder" | "select"
	Options     []string `json:"options,omitempty"` // for type=enum
	Source      string   `json:"source,omitempty"`  // for type=select: dynamic options endpoint (systemd_units, docker_containers, compose_projects, docker_images)
	Required    bool     `json:"required,omitempty"`
	Placeholder string   `json:"placeholder,omitempty"`
}

// schedDescriptor is one entry in the catalogue: a runner kind plus the
// metadata the UI needs to render and validate a scheduling form for it.
type schedDescriptor struct {
	Kind        string `json:"kind"`
	Label       string `json:"label"`
	Category    string `json:"category,omitempty"` // UI group (Operação, Backup & Dados, Segurança & Auditoria, Monitoramento, Higiene, Notificações, Rede)
	Description string `json:"description,omitempty"`
	// RequiresPrimary is a cosmetic hint for the UI (badge). It is NOT the
	// authority — the catalogue is already filtered by AuthorizedFor and the
	// runners revalidate on execution. Kept in sync with each runner's authz
	// by hand; the anti-drift test guards that every registered kind has an
	// explicit descriptor.
	RequiresPrimary bool `json:"requires_primary"`
	// Schedulable=false means the kind exists as a runner but doesn't belong
	// on the scheduler (e.g. jira_ai_analysis needs an interactive issue_key
	// and runs detached). It is excluded from the catalogue the UI consumes.
	Schedulable bool       `json:"schedulable"`
	Args        []schedArg `json:"args"`
	// Rich help fields for the info popup (the "i" button): what it does/why,
	// real examples, where the result lands and what to do next. Merged from
	// schedHelp in schedDescriptorFor.
	Details   string   `json:"details,omitempty"`
	UseCases  []string `json:"use_cases,omitempty"`
	Examples  []string `json:"examples,omitempty"`
	Output    string   `json:"output,omitempty"`
	NextSteps []string `json:"next_steps,omitempty"`
}

// schedDescriptors maps every registered runner kind to its scheduling
// descriptor. A kind absent here resolves to a non-schedulable fallback via
// schedDescriptorFor — so a newly-registered runner never silently appears in
// the UI with an empty form; it stays hidden until someone adds a descriptor.
var schedDescriptors = map[string]schedDescriptor{
	"apt_upgrade": {
		Kind:            "apt_upgrade",
		Label:           "Update system packages",
		Description:     "Runs apt-get update followed by upgrade -y.",
		RequiresPrimary: true,
		Schedulable:     true,
		Args:            []schedArg{},
	},
	"docker_pull": {
		Kind:        "docker_pull",
		Label:       "Pull Docker image",
		Description: "docker pull of a specific image.",
		Schedulable: true,
		Args: []schedArg{
			{Name: "ref", Label: "Imagem", Type: "select", Source: "docker_images", Required: true, Placeholder: "e.g. nginx:latest"},
		},
	},
	"docker_compose_pull": {
		Kind:        "docker_compose_pull",
		Label:       "Update a Compose project's images",
		Description: "docker compose pull in a project's directory.",
		Schedulable: true,
		Args: []schedArg{
			{Name: "dir", Label: "Compose project", Type: "select", Source: "compose_projects", Required: true, Placeholder: "/caminho/absoluto/do/projeto"},
		},
	},
	"image_prune": {
		Kind:            "image_prune",
		Label:           "Remove dangling Docker images",
		Description:     "docker image prune -af (deletes ALL untagged images on the host).",
		RequiresPrimary: true,
		Schedulable:     true,
		Args:            []schedArg{},
	},
	"backup_now": {
		Kind:            "backup_now",
		Label:           "Backup",
		Description:     "Packs the vault/config into a .tgz. Default destination: data/backups (leave empty to use the default).",
		RequiresPrimary: true,
		Schedulable:     true,
		Args: []schedArg{
			{Name: "target", Label: "What to save", Type: "enum", Required: true, Options: []string{"all", "vault", "config"}},
			{Name: "retention", Label: "Keep the last N (0 = all)", Type: "number", Placeholder: "ex.: 7"},
		},
	},
	"shell": {
		Kind:            "shell",
		Label:           "System command",
		Description:     "Runs a binary with arguments (no shell — no pipes/;/&).",
		RequiresPrimary: true,
		Schedulable:     true,
		Args: []schedArg{
			{Name: "cmd", Label: "Binary", Type: "string", Required: true, Placeholder: "e.g. /usr/bin/systemctl"},
			{Name: "args", Label: "Argumentos", Type: "string_list", Placeholder: "one argument per line"},
		},
	},
	"docker_restart": {
		Kind:            "docker_restart",
		Label:           "Restart Docker container",
		Description:     "docker restart of a container by name or id.",
		RequiresPrimary: true,
		Schedulable:     true,
		Args: []schedArg{
			{Name: "container", Label: "Container", Type: "select", Source: "docker_containers", Required: true, Placeholder: "container name or id"},
		},
	},
	"docker_compose_restart": {
		Kind:            "docker_compose_restart",
		Label:           "Restart a Compose project's services",
		Description:     "docker compose restart in a project's directory.",
		RequiresPrimary: true,
		Schedulable:     true,
		Args: []schedArg{
			{Name: "dir", Label: "Compose project", Type: "select", Source: "compose_projects", Required: true, Placeholder: "/caminho/absoluto/do/projeto"},
		},
	},
	"systemd_restart": {
		Kind:            "systemd_restart",
		Label:           "Restart service (systemd)",
		Description:     "systemctl restart or reload of a unit.",
		RequiresPrimary: true,
		Schedulable:     true,
		Args: []schedArg{
			{Name: "action", Label: "Ação", Type: "enum", Required: true, Options: []string{"restart", "reload"}},
			{Name: "unit", Label: "Service (unit)", Type: "select", Source: "systemd_units", Required: true, Placeholder: "e.g. nginx.service"},
		},
	},
	"docker_prune": {
		Kind:            "docker_prune",
		Label:           "Clean up Docker (volumes/networks/builder)",
		Description:     "Removes unused volumes, networks or build cache. (For images, use \"Remove dangling images\".)",
		RequiresPrimary: true,
		Schedulable:     true,
		Args: []schedArg{
			{Name: "scope", Label: "What to clean up", Type: "enum", Required: true, Options: []string{"volumes", "networks", "builder"}},
		},
	},
	"http_check": {
		Kind:            "http_check",
		Label:           "Check URL (health/uptime)",
		Description:     "Makes a GET to a URL and fails if it does not answer 2xx/3xx — handy to monitor a site or endpoint.",
		RequiresPrimary: true,
		Schedulable:     true,
		Args: []schedArg{
			{Name: "url", Label: "URL", Type: "string", Required: true, Placeholder: "https://exemplo.com/health"},
			{Name: "expect", Label: "Expected status (optional)", Type: "number", Placeholder: "e.g. 200 — empty = accepts 2xx/3xx"},
		},
	},
	"ssl_check": {
		Kind:            "ssl_check",
		Label:           "Check TLS/SSL certificate",
		Description:     "Connects to host:port and fails if the certificate expires soon (or already has).",
		RequiresPrimary: true,
		Schedulable:     true,
		Args: []schedArg{
			{Name: "host", Label: "Host", Type: "string", Required: true, Placeholder: "e.g. example.com"},
			{Name: "port", Label: "Porta", Type: "number", Placeholder: "443"},
			{Name: "warn_days", Label: "Warn if fewer than N days remain", Type: "number", Placeholder: "14"},
		},
	},
	"disk_check": {
		Kind:            "disk_check",
		Label:           "Check disk usage",
		Description:     "Fails if the partition goes over a usage limit (%).",
		RequiresPrimary: true,
		Schedulable:     true,
		Args: []schedArg{
			{Name: "path", Label: "Path", Type: "folder", Placeholder: "/ (default)"},
			{Name: "threshold", Label: "Usage limit (%)", Type: "number", Placeholder: "90"},
		},
	},
	"security_audit": {
		Kind:            "security_audit",
		Label:           "Security audit (Lynis)",
		Description:     "Runs lynis audit system --quick and reports hardening findings.",
		RequiresPrimary: true,
		Schedulable:     true,
		Args:            []schedArg{},
	},
	"rootkit_scan": {
		Kind:            "rootkit_scan",
		Label:           "Rootkit hunt",
		Description:     "Scans for rootkits; fails (alerts) if it finds anything suspicious.",
		RequiresPrimary: true,
		Schedulable:     true,
		Args: []schedArg{
			{Name: "tool", Label: "Ferramenta", Type: "enum", Required: true, Options: []string{"rkhunter", "chkrootkit"}},
		},
	},
	"integrity_check": {
		Kind:            "integrity_check",
		Label:           "File integrity (AIDE)",
		Description:     "aide --check; fails (alerts) if it detects a change against the baseline.",
		RequiresPrimary: true,
		Schedulable:     true,
		Args:            []schedArg{},
	},
	"trivy_scan": {
		Kind:            "trivy_scan",
		Label:           "Vulnerability scan (Trivy)",
		Description:     "Looks for CVEs in an image or folder; fails (alerts) if it finds a vulnerability.",
		RequiresPrimary: true,
		Schedulable:     true,
		Args: []schedArg{
			{Name: "scope", Label: "Alvo", Type: "enum", Required: true, Options: []string{"image", "fs"}},
			{Name: "target", Label: "Image or path", Type: "string", Required: true, Placeholder: "nginx:latest  or  /opt/app"},
			{Name: "severity", Label: "Severities (optional)", Type: "string", Placeholder: "HIGH,CRITICAL"},
		},
	},
	"fail2ban_report": {
		Kind:            "fail2ban_report",
		Label:           "fail2ban report",
		Description:     "fail2ban-client status (jails + bans); fails if the service is down.",
		RequiresPrimary: true,
		Schedulable:     true,
		Args:            []schedArg{},
	},
	"audit_report": {
		Kind:            "audit_report",
		Label:           "Audit snapshot",
		Description:     "Timers, cron, listening ports and recent logins — to diff and review.",
		RequiresPrimary: true,
		Schedulable:     true,
		Args:            []schedArg{},
	},
	"cleanup": {
		Kind:            "cleanup",
		Label:           "Free up space",
		Description:     "Housekeeping: apt cache, journal vacuum or old /tmp files.",
		RequiresPrimary: true,
		Schedulable:     true,
		Args: []schedArg{
			{Name: "scope", Label: "What to clean up", Type: "enum", Required: true, Options: []string{"apt_cache", "journal", "tmp"}},
		},
	},
	"cert_renew": {
		Kind:            "cert_renew",
		Label:           "Renew certificate (Let's Encrypt)",
		Description:     "certbot renew — renews the certificates that are close to expiring.",
		RequiresPrimary: true,
		Schedulable:     true,
		Args: []schedArg{
			{Name: "cert_name", Label: "Certificate (empty = all)", Type: "select", Source: "certbot_certs", Placeholder: "leave empty to renew them all"},
		},
	},
	"rclone_sync": {
		Kind:            "rclone_sync",
		Label:           "Sync folder → cloud",
		Description:     "rclone sync of a VPS folder to a remote (Drive/OneDrive/S3…).",
		RequiresPrimary: true,
		Schedulable:     true,
		Args: []schedArg{
			{Name: "source", Label: "Source folder (VPS)", Type: "folder", Required: true, Placeholder: "/opt/dados"},
			{Name: "remote", Label: "Remote (cloud)", Type: "select", Source: "rclone_remotes", Required: true},
			{Name: "remote_path", Label: "Folder on the remote", Type: "string", Placeholder: "backups/dados"},
		},
	},
	"docker_compose_up": {
		Kind:            "docker_compose_up",
		Label:           "Bring a Compose up (up -d)",
		Description:     "docker compose up -d in a project's directory.",
		RequiresPrimary: true,
		Schedulable:     true,
		Args: []schedArg{
			{Name: "dir", Label: "Compose project", Type: "select", Source: "compose_projects", Required: true, Placeholder: "/caminho/absoluto/do/projeto"},
		},
	},
	"git_pull": {
		Kind:            "git_pull",
		Label:           "Update repository (git pull)",
		Description:     "git -C <dir> pull --ff-only in a local repository.",
		RequiresPrimary: true,
		Schedulable:     true,
		Args: []schedArg{
			{Name: "dir", Label: "Repository directory", Type: "folder", Required: true, Placeholder: "/opt/app"},
		},
	},
	"apt_updatecheck": {
		Kind:            "apt_updatecheck",
		Label:           "Check for updates (without installing)",
		Description:     "Report of upgradable packages — installs nothing.",
		RequiresPrimary: true,
		Schedulable:     true,
		Args:            []schedArg{},
	},
	"reboot": {
		Kind:            "reboot",
		Label:           "Reboot the server",
		Description:     "Reboots the VPS (shutdown -r, with a 1-minute warning). Use with care.",
		RequiresPrimary: true,
		Schedulable:     true,
		Args:            []schedArg{},
	},
	"db_backup": {
		Kind:            "db_backup",
		Label:           "Database backup",
		Description:     "Dumps a Postgres/MySQL database into a .sql.gz (locally or in a folder you pick).",
		RequiresPrimary: true,
		Schedulable:     true,
		Args: []schedArg{
			{Name: "engine", Label: "Banco", Type: "enum", Required: true, Options: []string{"postgres", "mysql"}},
			{Name: "database", Label: "Database name", Type: "select", Source: "databases", Required: true, Placeholder: "e.g. myapp"},
			{Name: "dest", Label: "Destination folder (optional)", Type: "folder", Placeholder: "default: data/backups"},
			{Name: "retention", Label: "Keep the last N (0 = all)", Type: "number", Placeholder: "7"},
		},
	},
	"watchdog": {
		Kind:            "watchdog",
		Label:           "Disk + backup watchdog",
		Description:     "Fails (alerts) if the disk goes over the limit OR the newest backup gets old/corrupted.",
		RequiresPrimary: true,
		Schedulable:     true,
		Args: []schedArg{
			{Name: "disk_path", Label: "Disk path", Type: "folder", Placeholder: "/ (default)"},
			{Name: "disk_threshold", Label: "Disk limit (%)", Type: "number", Placeholder: "85"},
			{Name: "extra_path", Label: "Second mount point (optional)", Type: "folder", Placeholder: "/opt"},
			{Name: "backup_dir", Label: "Backups folder", Type: "folder", Placeholder: "default: data/backups"},
			{Name: "max_backup_age_hours", Label: "Max backup age (h)", Type: "number", Placeholder: "36"},
		},
	},
	"agent_routine": {
		Kind:            "agent_routine",
		Label:           "Agent routine (Claude)",
		Description:     "Fires a detached Claude session in a repository with a fixed prompt — e.g. nightly triage of new tickets, or reviewing the agents' work.",
		RequiresPrimary: true,
		Schedulable:     true,
		Args: []schedArg{
			{Name: "prompt", Label: "Prompt (what the agent should do)", Type: "string", Required: true, Placeholder: "e.g. triage the new Jira tickets and comment on each one"},
			{Name: "repo", Label: "Repository / worktree (cwd)", Type: "folder", Required: true, Placeholder: "/opt/app"},
			{Name: "model", Label: "Model (optional)", Type: "string", Placeholder: "empty = default (opus) — or opus/sonnet/haiku"},
			{Name: "session_name", Label: "Session name (optional)", Type: "string", Placeholder: "empty = generated automatically"},
		},
	},
	// jira_ai_analysis is AuthorizedFor→true for everyone, but it is NOT
	// schedulable: it needs an interactive issue_key and runs detached via
	// systemd-run. Mark it explicitly so the anti-drift test passes while the
	// catalogue keeps it out of the scheduler UI.
	"session_backup": {
		Kind:        "session_backup",
		Label:       "Backup de sessões do terminal",
		Description: "Saves the state of your sessions (windows, panes, each pane's folder and history) at the interval you choose. Pick 'All' and it looks at the sessions active at fire time and saves EACH ONE individually. Restore later from the session Backups tab.",
		Schedulable: true,
		Args: []schedArg{
			{Name: "session", Label: "Sessão", Type: "select", Source: "sessoes_do_usuario", Placeholder: "All = every active session, saved individually"},
			{Name: "retention", Label: "Keep the last N per session (0 = default)", Type: "number", Placeholder: "ex.: 24"},
		},
	},
	"jira_ai_analysis": {
		Kind:        "jira_ai_analysis",
		Label:       "Jira AI analysis",
		Schedulable: false,
		Args:        []schedArg{},
	},
}

// schedHelpEntry is the rich help shown in the info popup ("i" button).
type schedHelpEntry struct {
	Details   string
	UseCases  []string
	Examples  []string
	Output    string
	NextSteps []string
}

// jobLogNote is the common "where the result lives" line: every kind streams
// its full output to the queue, reachable from the schedule's 📄 button or the
// Jobs page.
const jobLogNote = "The full log of each run lives in Jobs — open it with the 📄 button on the schedule (or on the Jobs page). "

// schedHelp carries the long-form help for the info popup. Kept separate from
// the descriptor literals so the catalogue table stays compact; merged in by
// schedDescriptorFor.
var schedHelp = map[string]schedHelpEntry{
	"apt_upgrade": {
		Details:   "Updates the package index (apt-get update) and installs the available updates (apt-get upgrade -y), including OS security patches. It does not dist-upgrade (no kernel swap, no package removal).",
		UseCases:  []string{"Keep the server current on security fixes", "Shrink the exposure window to OS package CVEs"},
		Examples:  []string{"Every Monday 04:00, alerting only on failure", "Daily 03:30 on an exposed edge server"},
		Output:    jobLogNote + "Shows the upgraded packages. No artifact on disk.",
		NextSteps: []string{"If an upgrade asks for a reboot, schedule a reboot window or a 'Restart service' on the affected services", "Pair it with 'Audit snapshot' to record the before/after"},
	},
	"docker_pull": {
		Details:   "Pulls (docker pull) a specific image from the registry, leaving the newest version in the local cache — without restarting anything.",
		UseCases:  []string{"Pre-pull the new image before the maintenance window", "Keep a base image (e.g. nginx:latest) up to date locally"},
		Examples:  []string{"Pull nginx:latest every day at 02:00", "Update postgres:16 every night before the restart"},
		Output:    jobLogNote + "The image stays in Docker's local cache (visible on the Docker › Images page).",
		NextSteps: []string{"Schedule 'Restart container' or 'Compose restart' afterwards to bring up the new image", "Run 'Vulnerability scan (Trivy)' on the image you pulled"},
	},
	"docker_compose_pull": {
		Details:   "Runs docker compose pull in a project's directory, updating every image in compose.yml — without bringing up or restarting the services.",
		UseCases:  []string{"Update a stack's images at night and restart in the morning", "Make sure the next up uses the newest images"},
		Examples:  []string{"Pull the /opt/stacks/app project every night"},
		Output:    jobLogNote + "Shows which images were updated.",
		NextSteps: []string{"Chain it with 'Restart a Compose project's services' on the same project to apply them"},
	},
	"image_prune": {
		Details:   "docker image prune -af: removes ALL untagged (dangling) and unused images from the host. Destructive and host-wide — it affects every container.",
		UseCases:  []string{"Reclaim disk space automatically", "Clear orphan layers left by frequent builds"},
		Examples:  []string{"Weekly, Sunday 05:00"},
		Output:    jobLogNote + "Shows how much space was reclaimed.",
		NextSteps: []string{"Follow it with 'Check disk usage' to confirm the gain", "For volumes/networks, use 'Clean up Docker'"},
	},
	"backup_now": {
		Details:   "Packs the vault and/or configuration into a .tgz. Destination on the VPS itself (with a folder browser) or in the cloud through rclone (Drive/OneDrive/S3/Dropbox). Supports retention (keep only the last N).",
		UseCases:  []string{"Daily vault backup to a safe folder or the cloud", "A snapshot before big changes", "Automatic off-site copy with retention"},
		Examples:  []string{"Daily 03:00, target 'all', keep the last 7, destination Google Drive", "Weekly 'vault' to /mnt/backup, retention 4"},
		Output:    jobLogNote + "The vpsm-backup-<target>-<date>.tgz file is saved to the DESTINATION you chose (a VPS folder or a cloud remote) — the exact path appears at the end of the log.",
		NextSteps: []string{"Check the .tgz path in the log (📄 button)", "Test a restore now and then — a backup with no tested restore is not a backup", "Use retention so the disk does not fill up"},
	},
	"shell": {
		Details:   "Runs a binary with explicit arguments (no shell — no pipes, ;, &, $). An escape hatch for your own automations.",
		UseCases:  []string{"Run your own maintenance script at a fixed time", "Fire an application command periodically"},
		Examples:  []string{"/usr/bin/systemctl reload nginx", "/opt/app/scripts/rotate.sh (binary + args kept separate)"},
		Output:    jobLogNote + "Captures the command's stdout+stderr.",
		NextSteps: []string{"Leave 'alerts: failures only' on to be told if the command exits with an error"},
	},
	"docker_restart": {
		Details:   "docker restart of a container by name or id. A controlled restart of one specific service.",
		UseCases:  []string{"Restart an app that leaks memory", "Recover a service that hangs until the root cause is fixed"},
		Examples:  []string{"Restart 'my-app' every day at 04:00", "After 'Pull image', restart the container that uses it"},
		Output:    jobLogNote,
		NextSteps: []string{"Chain it after a 'Pull image' to apply the new version", "Add a 'Check URL' a few minutes later to confirm it came back up"},
	},
	"docker_compose_restart": {
		Details:   "docker compose restart in a project's directory — restarts every service in the stack at once.",
		UseCases:  []string{"Recycle a stack during a low-traffic window", "Apply config that needs a restart off-peak"},
		Examples:  []string{"Restart the /opt/stacks/app project on Sunday 05:00"},
		Output:    jobLogNote,
		NextSteps: []string{"Pair it with 'Compose pull' beforehand to bring up new images", "A 'Check URL' afterwards confirms it is healthy"},
	},
	"systemd_restart": {
		Details:   "systemctl restart or reload of a unit. Reload applies config without dropping connections (when the unit supports it); restart restarts the service.",
		UseCases:  []string{"reload nginx/caddy after renewing a certificate", "nightly restart of a service with a known memory leak"},
		Examples:  []string{"reload nginx.service after the 'Check certificate'", "restart my-daemon.service daily at 04:00"},
		Output:    jobLogNote,
		NextSteps: []string{"Prefer 'reload' when the unit supports it (zero downtime)", "A 'Check URL' afterwards proves the service answered"},
	},
	"docker_prune": {
		Details:   "Removes Docker volumes, networks or build cache that are not in use. (For images, use 'Remove dangling images'.) Destructive and host-wide.",
		UseCases:  []string{"Clear build cache that grows with local CI", "Remove orphan volumes from containers already deleted"},
		Examples:  []string{"Clean up 'builder' weekly", "Monthly prune of orphan 'networks'"},
		Output:    jobLogNote + "Shows what was removed and how much space was freed.",
		NextSteps: []string{"Confirm the gain with 'Check disk usage'", "BE CAREFUL with 'volumes': it only removes unused ones, but make sure nothing important is left loose"},
	},
	"http_check": {
		Details:   "Makes a GET to a URL and FAILS (fires an alert) if the response is not 2xx/3xx — or if it differs from an expected status. A simple uptime monitor.",
		UseCases:  []string{"Watch whether a site/endpoint is up", "Catch it when it starts returning 5xx", "Confirm a service came back after a deploy"},
		Examples:  []string{"GET https://mysite.com/health every 5 min", "Expect status 200 from an API every 10 min"},
		Output:    jobLogNote + "Shows the HTTP status and response time of every check.",
		NextSteps: []string{"Leave 'alerts: failures only' on to be told the moment it goes down", "If it drops often, chain a 'Restart service' on the service behind it"},
	},
	"ssl_check": {
		Details:   "Connects to host:port over TLS, reads the certificate and FAILS (alerts) if it expires in fewer than N days (or already has). Catches a failed renewal before the site breaks.",
		UseCases:  []string{"Warn 14 days before a certificate expires", "Monitor several domains", "Catch a certbot that stopped renewing"},
		Examples:  []string{"mysite.com:443, warn at 14 days, check daily", "api.example.com:8443, warn at 30 days"},
		Output:    jobLogNote + "Shows the CN, issuer and expiry date (and how many days are left).",
		NextSteps: []string{"When alerted, renew the cert and then 'reload' nginx/caddy through 'Restart service'", "Create one ssl_check per important domain"},
	},
	"disk_check": {
		Details:   "Measures partition usage and FAILS (alerts) if it goes over a limit (%). One-off and logged — it complements the continuous metric alert.",
		UseCases:  []string{"Alert when the disk goes over 90%", "Watch a specific partition (e.g. /var) that grows with logs"},
		Examples:  []string{"/ with a 90% limit, hourly", "/var with an 85% limit, daily"},
		Output:    jobLogNote + "Shows used/free in GiB and the percentage.",
		NextSteps: []string{"When it alerts, run 'Free up space' (journal/apt/tmp) or 'Clean up Docker'", "Track down the biggest consumer before it fills up"},
	},
	"watchdog": {
		Details:   "A single check that joins two host health signals: disk usage above the limit AND a newest backup that is old/corrupted. It FAILS (fires an alert) on either condition.",
		UseCases:  []string{"Watch disk and backup freshness in one go", "Make sure the daily backup is running AND intact", "A single host-health alert"},
		Examples:  []string{"Disk / at 85%, backup in data/backups no older than 36h, hourly", "Disks / and /opt, daily backup, check every 6h"},
		Output:    jobLogNote + "Shows disk usage (used/free/%), the age of the newest backup and the result of the gzip -t integrity check.",
		NextSteps: []string{"Disk alert -> run 'Free up space' or 'Clean up Docker'", "Backup alert -> check the 'Back up now'/'Session backup' schedule"},
	},
	"security_audit": {
		Details:   "Runs Lynis (lynis audit system --quick): a broad hardening audit — permissions, accounts, firewall, services, compliance. It reports findings and suggestions.",
		UseCases:  []string{"Weekly/monthly security posture review", "Track how hardening evolves", "A checklist before a compliance audit"},
		Examples:  []string{"Weekly, Sunday 02:00", "Monthly, the 1st at 03:00"},
		Output:    jobLogNote + "The Lynis report (warnings + suggestions + hardening index) goes to the log. Lynis's own details are in /var/log/lynis.log and /var/log/lynis-report.dat.",
		NextSteps: []string{"Work through the highest-impact suggestions between runs", "Compare the hardening index over time to see progress"},
	},
	"rootkit_scan": {
		Details:   "Scans for rootkits and malware with rkhunter or chkrootkit. FAILS (alerts) if it finds anything suspicious.",
		UseCases:  []string{"Early detection of compromise, daily", "A defense layer on exposed servers"},
		Examples:  []string{"rkhunter daily at 01:00", "chkrootkit weekly as a second opinion"},
		Output:    jobLogNote + "Lists the warnings it found. rkhunter's own log is in /var/log/rkhunter.log.",
		NextSteps: []string{"Every warning is to be INVESTIGATED (it may be a false positive after updates — run 'rkhunter --propupd' after upgrading)", "If it is confirmed, isolate the host and investigate"},
	},
	"integrity_check": {
		Details:   "aide --check: compares the filesystem against a baseline (AIDE) and FAILS (alerts) if something critical changed — binaries, /etc, crontabs. Requires an initialized baseline (aide --init).",
		UseCases:  []string{"File integrity monitoring (detect tampering)", "Alert on unauthorized changes in /etc or crontabs"},
		Examples:  []string{"Daily 02:30, after the maintenance window"},
		Output:    jobLogNote + "Lists the files added/removed/changed against the baseline.",
		NextSteps: []string{"If the change is legitimate (after an upgrade), update the baseline (aide --update)", "If it was NOT expected, treat it as an incident"},
	},
	"trivy_scan": {
		Details:   "Trivy looks for CVEs (known vulnerabilities) in a Docker image or a folder. With --exit-code 1 it FAILS (alerts) if it finds a vulnerability at the chosen severity.",
		UseCases:  []string{"Re-scan production images against new CVEs (published daily)", "Fail if a HIGH/CRITICAL CVE shows up", "Audit a directory's dependencies"},
		Examples:  []string{"image nginx:latest, severity HIGH,CRITICAL, daily", "fs /opt/app weekly"},
		Output:    jobLogNote + "Lists the CVEs (package, version, severity, available fix).",
		NextSteps: []string{"When you find a CVE with a fix, schedule 'Pull image' of the fixed version + 'Restart container'", "Prioritize CRITICAL/HIGH with an available fix"},
	},
	"fail2ban_report": {
		Details:   "fail2ban-client status: lists the active jails and banned IPs. FAILS if the service is down — a fail2ban that is not running is a silent exposure.",
		UseCases:  []string{"A report of blocked brute-force attempts", "Make sure fail2ban is still running"},
		Examples:  []string{"Daily 08:00 to review the night", "Every 6h as the service's heartbeat"},
		Output:    jobLogNote + "Lists the jails and the ban counts.",
		NextSteps: []string{"If the service is down, bring it back with 'Restart service' (fail2ban.service)", "Ban spikes can mean an attack is under way"},
	},
	"audit_report": {
		Details:   "A read-only snapshot for the audit trail: systemd timers, crontab, listening ports (ss) and recent logins (last). It always succeeds — it exists to be diffed month over month to spot changes.",
		UseCases:  []string{"Monthly review of what is scheduled/listening on the host", "Catch a new cron/timer/port that should not exist", "A record for post-incident investigation"},
		Examples:  []string{"Monthly, the 1st at 06:00", "Weekly for sensitive hosts"},
		Output:    jobLogNote + "The whole snapshot (timers/cron/ports/logins) goes to the log — compare it with last month's to see what changed.",
		NextSteps: []string{"Compare it with the previous run (📄 button) — any new line is worth investigating", "Spotted something odd? cross-check with 'Rootkit hunt' and 'File integrity'"},
	},
	"cleanup": {
		Details:   "Space housekeeping: clears the apt cache, vacuums the journal (systemd logs) or deletes old files from /tmp. Pick the scope.",
		UseCases:  []string{"Keep /var/log/journal under control", "Free the apt cache after upgrades", "Clear /tmp of files older than 7 days"},
		Examples:  []string{"weekly journal vacuum", "apt_cache after 'Update packages'"},
		Output:    jobLogNote + "Shows how much space was freed.",
		NextSteps: []string{"Pair it with 'Check disk usage' to measure the effect", "Chain 'apt_cache' right after 'Update system packages'"},
	},
	"cert_renew": {
		Details:   "Runs certbot renew (non-interactive): renews the Let's Encrypt certificates that are close to expiring. Leave the certificate empty to renew them all, or pick a specific one.",
		UseCases:  []string{"Make sure certificates never expire (automatic renewal)", "Force the renewal of one specific domain"},
		Examples:  []string{"Daily 03:30 (certbot only renews what is close to expiring)", "Renew only 'mysite.com' twice a week"},
		Output:    jobLogNote + "Shows which certificates were renewed (or 'not yet due').",
		NextSteps: []string{"After renewing, 'reload' nginx/caddy through 'Restart service' to serve the new cert", "Pair it with 'Check TLS/SSL certificate' to monitor validity"},
	},
	"rclone_sync": {
		Details:   "rclone sync mirrors a VPS folder to a cloud remote (Drive/OneDrive/S3/Dropbox…). CAREFUL: sync makes the destination IDENTICAL to the source — extra files at the destination are deleted.",
		UseCases:  []string{"Continuous off-site copy of a data/uploads folder", "Mirror local backups to the cloud every day"},
		Examples:  []string{"/opt/data → gdrive:backups/data, daily 02:00", "/var/www/uploads → s3remote:site-uploads, every 6h"},
		Output:    jobLogNote + "Shows what was transferred. The files land on the remote you chose.",
		NextSteps: []string{"To send WITHOUT deleting anything at the destination, prefer 'Backup' (which adds a .tgz)", "Check on the destination's rclone that the files arrived"},
	},
	"docker_compose_up": {
		Details:   "docker compose up -d in a project's directory: brings up/recreates the services that are down or outdated, in the background.",
		UseCases:  []string{"Keep a stack always up (self-heal)", "Apply changes after a 'Compose pull'"},
		Examples:  []string{"up -d of the /opt/stacks/app project after the nightly pull"},
		Output:    jobLogNote + "Shows which services came up or were recreated.",
		NextSteps: []string{"Chain it after 'Compose pull' to apply new images", "A 'Check URL' afterwards confirms it is healthy"},
	},
	"git_pull": {
		Details:   "git -C <dir> pull --ff-only in a local repository. It only advances on a fast-forward (it never creates a merge nor overwrites local work).",
		UseCases:  []string{"Keep a content/config repo in sync with the remote", "Update a static site versioned in git"},
		Examples:  []string{"Pull /opt/site every hour", "Pull /opt/infra every night"},
		Output:    jobLogNote + "Shows the commits it brought in (or 'Already up to date').",
		NextSteps: []string{"Chain a 'Restart service'/'Compose restart' if the pull requires a reload", "If it fails on a conflict, resolve it by hand on the server"},
	},
	"apt_updatecheck": {
		Details:   "Updates the index and lists the upgradable packages — it installs NOTHING. Visibility into what is pending before you apply it.",
		UseCases:  []string{"A weekly report of what there is to update", "Know the size of the patch backlog without touching the system"},
		Examples:  []string{"Every Monday 08:00 (review before approving the upgrade)"},
		Output:    jobLogNote + "Lists the packages with current → available version.",
		NextSteps: []string{"To apply them, use 'Update system packages'", "Pair it with an alert to be told when too many are pending"},
	},
	"reboot": {
		Details:   "Reboots the VPS with shutdown -r +1 (a 1-minute warning). Use with care: it takes EVERY service down until the machine is back. Make sure everything essential comes up on its own at boot.",
		UseCases:  []string{"Monthly maintenance reboot (applies a new kernel)", "Recycle the host after a big upgrade window"},
		Examples:  []string{"The 1st of each month at 05:00 (maintenance window)", "After an 'Update packages' that asked for a reboot"},
		Output:    jobLogNote + "Records the scheduled reboot; the VPS restarts about a minute later.",
		NextSteps: []string{"Confirm the services/containers restart automatically at boot", "A 'Check URL' minutes later (from another host) would confirm it came back — here the app restarts too"},
	},
	"db_backup": {
		Details:   "A logical dump of a Postgres database (pg_dump as the postgres user) or MySQL (mysqldump over the root socket), saved compressed as .sql.gz. Requires the tool installed and local auth working.",
		UseCases:  []string{"Daily backup of the application database", "A snapshot before a schema migration"},
		Examples:  []string{"postgres 'myapp' daily 03:00, keep 7", "mysql 'wordpress' every 12h"},
		Output:    jobLogNote + "The <engine>-<database>-<date>.sql.gz file lands at the destination (data/backups by default). Restore it with gunzip + psql/mysql.",
		NextSteps: []string{"Send the .sql.gz to the cloud with 'Sync folder → cloud'", "Test a restore now and then"},
	},
	"session_backup": {
		Details:   "A snapshot of YOUR sessions, \"resurrect lite\" style: the structure (windows/panes), each pane's working directory, the foreground command and the recent scrollback (history). It does NOT resurrect live processes — restoring recreates the structure with the same cwds and pastes the history as context into a fresh shell (a limit of the model, with no way around it). With 'All', at fire time it looks at the ACTIVE sessions and saves EACH ONE as a separate backup (one session per backup), so every session has its own timeline and the 'keep the last N' retention applies PER SESSION. You can also schedule one specific session.",
		UseCases:  []string{"A daily backup of each active session, individually, at the time you choose", "Not losing the context of your work sessions if the VPS reboots", "Saving each session hourly during working hours"},
		Examples:  []string{"All sessions daily at 02:00, keeping the last 7 of each", "All of them every hour, retention 24 per session", "Only the 'main' session every 30 min"},
		Output:    jobLogNote + "One backup PER session lands on the session Backups tab (per user) — restore whichever you want from there. It creates no file in the system backups folder.",
		NextSteps: []string{"Restore from the session Backups tab (Restore button)", "Tune the retention (per session) to balance history against disk space", "The per-session retention is independent of the general automatic backup"},
	},
	"agent_routine": {
		Details:   "On every fire it creates a DETACHED Claude session (dtach, shielded in user.slice) in the repository/worktree you chose and pastes the prompt as the first message — the agent starts working on its own. It does not block: the session stays alive and you attach from the terminal whenever you want to follow it. Only the primary can schedule it (the session runs as root). Leave the name empty to generate one automatically (vpsm-routine-<timestamp>).",
		UseCases:  []string{"Nightly triage of new Jira tickets", "Daily review of the agents' work", "A maintenance/cleanup routine for a repository"},
		Examples:  []string{"Every day 03:00: 'triage the new tickets and comment on each one' in /opt/app", "Monday 08:00: 'review the PRs the agents opened in the last week'"},
		Output:    jobLogNote + "The job records the name of the session it created; the agent's work happens inside it (attach from the terminal).",
		NextSteps: []string{"Open the terminal and attach to the session to follow it or step in", "Pair it with spend caps (vpsmctl agent-budget) to be alerted if it costs too much"},
	},
}

// schedCategory groups kinds for the UI (collapsible sections + filter). Kept
// out of the literals so the table stays compact; merged in schedDescriptorFor.
// A kind absent here falls under "Other".
var schedCategory = map[string]string{
	"apt_upgrade": "Operations", "docker_pull": "Operations", "docker_compose_pull": "Operations",
	"docker_restart": "Operations", "docker_compose_restart": "Operations", "docker_compose_up": "Operations",
	"systemd_restart": "Operations", "image_prune": "Operations", "docker_prune": "Operations",
	"shell": "Operations", "reboot": "Operations", "git_pull": "Operations",
	"agent_routine": "Operations",
	"backup_now":    "Backup & Data", "db_backup": "Backup & Data", "rclone_sync": "Backup & Data",
	"session_backup": "Backup & Data",
	"security_audit": "Security & Audit", "rootkit_scan": "Security & Audit",
	"integrity_check": "Security & Audit", "trivy_scan": "Security & Audit",
	"fail2ban_report": "Security & Audit", "audit_report": "Security & Audit",
	"cert_renew": "Security & Audit",
	"ssl_check":  "Monitoring", "disk_check": "Monitoring", "http_check": "Monitoring",
	"apt_updatecheck": "Monitoring", "watchdog": "Monitoring",
	"cleanup":        "Housekeeping",
	"notify_message": "Notifications",
}

// schedDescriptorFor returns the explicit descriptor for a kind, or a safe
// non-schedulable fallback so unknown/new kinds never crash the catalogue and
// never leak into the UI with a blank form. Rich help + category merged in.
func schedDescriptorFor(kind string) schedDescriptor {
	d, ok := schedDescriptors[kind]
	if !ok {
		return schedDescriptor{Kind: kind, Label: kind, Schedulable: false, Args: []schedArg{}}
	}
	if c, ok := schedCategory[kind]; ok {
		d.Category = c
	} else {
		d.Category = "Other"
	}
	if h, ok := schedHelp[kind]; ok {
		d.Details = h.Details
		d.UseCases = h.UseCases
		d.Examples = h.Examples
		d.Output = h.Output
		d.NextSteps = h.NextSteps
	}
	return d
}

// handleSchedulerCatalog serves GET /api/scheduler/catalog: the schedulable
// kinds THIS user is authorized to run, sorted for stable output. The UI binds
// its task picker + dynamic form to this, so it can never offer a kind the
// server would reject (closing the dropdown-drift + bypass holes at the source).
func (r *Router) handleSchedulerCatalog(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		writeErr(w, 405, "method not allowed")
		return
	}
	user := auth.UserFrom(req)
	if user == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	isPrim := r.isPrimary(user)

	// Snapshot the registered kinds under the cfg lock, then release before
	// calling queueRunner (which re-locks) to avoid a self-deadlock.
	r.cfgMu.Lock()
	kinds := make([]string, 0, len(r.queueRunners))
	for k := range r.queueRunners {
		kinds = append(kinds, k)
	}
	r.cfgMu.Unlock()
	sort.Strings(kinds)

	out := make([]schedDescriptor, 0, len(kinds))
	for _, k := range kinds {
		runner, ok := r.queueRunner(k)
		if !ok {
			continue
		}
		if !runner.AuthorizedFor(user, isPrim) {
			continue
		}
		d := schedDescriptorFor(k)
		if !d.Schedulable {
			continue
		}
		out = append(out, d)
	}
	writeJSON(w, map[string]any{"kinds": out, "is_primary": isPrim})
}

// authorizeKind gates a scheduler mutation by the same Runner.AuthorizedFor
// check the queue's enqueue path uses (handlers_queue.go:70-77). It writes the
// HTTP error + audit event and returns false when the user may not use kind, so
// callers just `if !r.authorizeKind(...) { return }`. This is the HTTP half of
// the defense-in-depth bypass fix; the tick's autonomous half lives in
// scheduler.fire() via SetAuthorizer.
func (r *Router) authorizeKind(w http.ResponseWriter, req *http.Request, user, kind string) bool {
	runner, ok := r.queueRunner(kind)
	if !ok {
		writeErr(w, 400, "unknown kind: "+kind)
		return false
	}
	if !runner.AuthorizedFor(user, r.isPrimary(user)) {
		r.auditEvent(req, user, "scheduler.denied", "kind="+kind)
		writeErr(w, 403, "no permission for kind "+kind)
		return false
	}
	return true
}
