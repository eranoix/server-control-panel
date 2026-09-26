package deploy

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

// Preview envs: a push on a branch other than the production branch brings up
// an isolated EPHEMERAL stack (its own compose project, its own port, its own
// vhost), with a TTL and a ceiling on concurrency. Deleting the branch (or the
// TTL running out) brings it down.
const (
	PreviewTTLHours = 48 // max age of a preview before the reaper takes it down
	MaxPreviews     = 3  // simultaneous previews per app
)

// PreviewTTL is the TTL as a Duration.
func PreviewTTL() time.Duration { return PreviewTTLHours * time.Hour }

var previewSlugRe = regexp.MustCompile(`[^a-z0-9]+`)

// PreviewSlug derives a DNS-safe slug from the branch name (feature/foo → feature-foo).
func PreviewSlug(branch string) string {
	b := strings.ToLower(strings.TrimPrefix(branch, "refs/heads/"))
	b = previewSlugRe.ReplaceAllString(b, "-")
	b = strings.Trim(b, "-")
	if len(b) > 24 {
		b = strings.Trim(b[:24], "-")
	}
	return b
}

// ActivePreviews maps slug → started (unix) of the newest preview deploy that
// is still up (it has a running record and was not torn down).
func ActivePreviews(app App) map[string]int64 {
	running := map[string]int{}
	started := map[string]int64{}
	for _, d := range app.Deploys {
		if d.Preview == "" || d.Status != "running" {
			continue
		}
		running[d.Preview]++
		if d.Started > started[d.Preview] {
			started[d.Preview] = d.Started
		}
	}
	out := map[string]int64{}
	for slug, n := range running {
		if n > 0 {
			out[slug] = started[slug]
		}
	}
	return out
}

// CanDeployPreview checks the ceiling on simultaneous previews. A slug that is
// already active may redeploy; a new slug only gets through if there is room.
func CanDeployPreview(app App, slug string) (bool, string) {
	active := ActivePreviews(app)
	if _, exists := active[slug]; exists {
		return true, ""
	}
	if len(active) >= MaxPreviews {
		return false, fmt.Sprintf("cap of %d previews reached (active: %d) — close one first", MaxPreviews, len(active))
	}
	return true, ""
}

// ReapPreviews brings down previews whose newest deploy is past the TTL.
// Returns how many were taken down. Called by the scheduled runner
// deploy_preview_reap.
func ReapPreviews(ctx context.Context, st *Store, logW io.Writer) (int, error) {
	apps, err := st.List()
	if err != nil {
		return 0, err
	}
	cutoff := time.Now().Add(-PreviewTTL()).Unix()
	n := 0
	for _, app := range apps {
		for slug, started := range ActivePreviews(app) {
			if started < cutoff {
				fmt.Fprintf(logW, "reap preview %s/%s (age > %dh)\n", app.Name, slug, PreviewTTLHours)
				_ = TeardownPreview(ctx, st, app, slug, logW)
				n++
			}
		}
	}
	return n, nil
}
