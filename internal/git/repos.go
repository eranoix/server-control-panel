package git

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"server-control-panel/internal/config"
)

// defaultRepos is the built-in seed of the allowlist, used when
// config.GitRepos is empty. It reflects the policy the user confirmed:
//   - vps-manager : strongly read-only (personal remote + it is the app itself).
//   - northwind-web: write, WORK identity.
//   - acme-booking: write, PERSONAL identity (never a work e-mail).
//   - venice-cli / supabase: read-only (upstream forks).
//
// Future repos come in through config.GitRepos (merged by Path in
// effectiveRepos) — each with its own policy and identity.
func defaultRepos() []config.GitRepo {
	return []config.GitRepo{
		// vps-manager: fully writable at the user's request. Personal identity
		// (it matches the northwind-dev remote and the history). Safety against clashing
		// with the deploy flow: the writes use withRepoWriteLock (mutex +
		// flock .git/vpsm-git.lock + an index.lock check) and the commits land
		// on refactor/foundation itself (canon), which `agentctl deploy`
		// converges — so it neither diverges nor clobbers.
		{ID: "vps-manager", Path: "/opt/panel", Name: "VPS Manager", Policy: policyWrite,
			ExpName: "northwind-dev", ExpEmail: "sam.rivera@personal.example"},
		{ID: "northwind-web", Path: "/root/projetos/northwind-web", Name: "Northwind Web",
			Policy: policyWrite, ExpName: "Sam Rivera", ExpEmail: "sam@northwind.example"},
		{ID: "css-lee", Path: "/root/projetos/acme-booking", Name: "Acme Booking",
			Policy: policyWrite, ExpName: "northwind-dev", ExpEmail: "sam.rivera@personal.example"},
		// venice-cli and supabase removed at the user's request (they do not show up in Git).
	}
}

const (
	policyReadOnly = "read-only"
	policyWrite    = "write"
)

// defaultIdentities is the seed of selectable author identities, used when
// config.GitIdentities is empty. It reflects the user's known identities
// (work/personal); new ones come in through config.
func defaultIdentities() []config.GitIdentity {
	return []config.GitIdentity{
		{ID: "work", Label: "Work — Northwind", Name: "Sam Rivera", Email: "sam@northwind.example"},
		{ID: "personal", Label: "Pessoal", Name: "northwind-dev", Email: "sam.rivera@personal.example"},
	}
}

// effectiveIdentities = seed + config.GitIdentities merged by ID.
func effectiveIdentities(cfg *config.Config) []config.GitIdentity {
	out := defaultIdentities()
	idx := map[string]int{}
	for i, id := range out {
		idx[id.ID] = i
	}
	if cfg != nil {
		for _, id := range cfg.GitIdentities {
			if id.ID == "" || id.Email == "" {
				continue
			}
			if i, ok := idx[id.ID]; ok {
				out[i] = id
			} else {
				idx[id.ID] = len(out)
				out = append(out, id)
			}
		}
	}
	return out
}

// resolveIdentity finds an identity by ID; ok=false when it does not exist.
func resolveIdentity(cfg *config.Config, id string) (config.GitIdentity, bool) {
	for _, x := range effectiveIdentities(cfg) {
		if x.ID == id {
			return x, true
		}
	}
	return config.GitIdentity{}, false
}

// effectiveRepos resolves the final allowlist: the built-in seed as the
// baseline, with config.GitRepos merged by Path (a config entry overrides the
// policy/identity of a seed with the same path; new paths are appended).
// That makes "adding a future repo" one entry in config, without losing the
// seed; and a seed repo's policy can be hardened/relaxed from config.
func effectiveRepos(cfg *config.Config) []config.GitRepo {
	out := defaultRepos()
	idx := map[string]int{}
	for i, r := range out {
		idx[filepath.Clean(r.Path)] = i
	}
	if cfg != nil {
		for _, r := range cfg.GitRepos {
			if r.Path == "" {
				continue
			}
			key := filepath.Clean(r.Path)
			if i, ok := idx[key]; ok {
				// Override: fields the override leaves unset keep the seed's value.
				if r.ID == "" {
					r.ID = out[i].ID
				}
				if r.Name == "" {
					r.Name = out[i].Name
				}
				if r.Policy == "" {
					r.Policy = out[i].Policy
				}
				out[i] = r
			} else {
				if r.ID == "" {
					r.ID = slugFromPath(r.Path)
				}
				if r.Policy == "" {
					r.Policy = policyReadOnly // safe default: a new repo is born read-only
				}
				idx[key] = len(out)
				out = append(out, r)
			}
		}
	}
	return out
}

func slugFromPath(p string) string {
	return filepath.Base(filepath.Clean(p))
}

// resolveRepo locates an allowlist repo by ID and confirms it is still a git
// repository on disk (.git as a file OR a directory — worktrees use a file).
// It returns (repo, true) only when both hold; it is the boundary that makes
// up for the absence of a directory jail elsewhere in the app.
func resolveRepo(cfg *config.Config, id string) (config.GitRepo, bool) {
	if id == "" {
		return config.GitRepo{}, false
	}
	for _, r := range effectiveRepos(cfg) {
		if r.ID != id {
			continue
		}
		if !isGitRepo(r.Path) {
			return config.GitRepo{}, false
		}
		return r, true
	}
	return config.GitRepo{}, false
}

// isGitRepo confirms the presence of <path>/.git as a file or a directory.
func isGitRepo(path string) bool {
	if path == "" {
		return false
	}
	fi, err := os.Stat(filepath.Join(path, ".git"))
	if err != nil {
		return false
	}
	return fi.IsDir() || fi.Mode().IsRegular()
}

// canWrite reports whether the repo's policy allows writing.
func canWrite(r config.GitRepo) bool {
	return r.Policy == policyWrite
}

// repoStatus is the per-repo summary exposed at GET /repos: branch state,
// dirtiness, divergence from upstream, and the match between the effective git
// identity (the live local config) and the one the allowlist expects.
type repoStatus struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Path              string `json:"path"`
	Policy            string `json:"policy"`
	Writable          bool   `json:"writable"`
	CurrentBranch     string `json:"current_branch"`
	Detached          bool   `json:"detached"`
	Dirty             bool   `json:"dirty"`
	Ahead             int    `json:"ahead"`
	Behind            int    `json:"behind"`
	EffectiveIdentity string `json:"effective_identity"`
	ExpectedIdentity  string `json:"expected_identity"`
	IdentityOK        bool   `json:"identity_ok"`
	Error             string `json:"error,omitempty"`
}

// describeRepo assembles a repo's repoStatus. It never propagates raw stderr:
// on a local failure it returns a status with Error filled in and the rest
// zeroed, so that one problematic repo does not take down the whole listing.
func describeRepo(ctx context.Context, r config.GitRepo) repoStatus {
	st := repoStatus{
		ID: r.ID, Name: r.Name, Path: r.Path, Policy: r.Policy,
		Writable:         canWrite(r),
		ExpectedIdentity: formatIdentity(r.ExpName, r.ExpEmail),
	}

	// Current branch / detached.
	if res, err := run(ctx, r.Path, "symbolic-ref", "--quiet", "--short", "HEAD"); err == nil && res.Code == 0 {
		st.CurrentBranch = strings.TrimSpace(res.Stdout)
	} else {
		st.Detached = true
		if res, err := run(ctx, r.Path, "rev-parse", "--short", "HEAD"); err == nil && res.Code == 0 {
			st.CurrentBranch = strings.TrimSpace(res.Stdout)
		}
	}

	// Dirtiness (any line in porcelain = dirty).
	if res, err := run(ctx, r.Path, "status", "--porcelain"); err == nil && res.Code == 0 {
		st.Dirty = strings.TrimSpace(res.Stdout) != ""
	}

	// Ahead/behind vs upstream (silent when there is no upstream).
	if res, err := run(ctx, r.Path, "rev-list", "--left-right", "--count", "@{upstream}...HEAD"); err == nil && res.Code == 0 {
		fields := strings.Fields(strings.TrimSpace(res.Stdout))
		if len(fields) == 2 {
			st.Behind, _ = strconv.Atoi(fields[0])
			st.Ahead, _ = strconv.Atoi(fields[1])
		}
	}

	// The live effective identity (the repo's local config).
	en := configValue(ctx, r.Path, "user.name")
	ee := configValue(ctx, r.Path, "user.email")
	st.EffectiveIdentity = formatIdentity(en, ee)

	// identity_ok: only meaningful for writable repos that define an expected
	// identity. Read-only is always "ok" (nothing is going to be committed).
	if !canWrite(r) || r.ExpEmail == "" {
		st.IdentityOK = true
	} else {
		st.IdentityOK = strings.EqualFold(ee, r.ExpEmail)
	}
	return st
}

// configValue reads a local git config (empty when absent).
func configValue(ctx context.Context, repoPath, key string) string {
	res, err := run(ctx, repoPath, "config", "--local", "--get", key)
	if err != nil || res.Code != 0 {
		return ""
	}
	return strings.TrimSpace(res.Stdout)
}

func formatIdentity(name, email string) string {
	name = strings.TrimSpace(name)
	email = strings.TrimSpace(email)
	switch {
	case name == "" && email == "":
		return ""
	case email == "":
		return name
	case name == "":
		return email
	default:
		return name + " <" + email + ">"
	}
}
