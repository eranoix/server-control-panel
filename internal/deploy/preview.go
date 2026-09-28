package deploy

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

const (
	PreviewTTLHours = 48
	MaxPreviews     = 3
)

func PreviewTTL() time.Duration { return PreviewTTLHours * time.Hour }

var previewSlugRe = regexp.MustCompile(`[^a-z0-9]+`)

func PreviewSlug(branch string) string {
	b := strings.ToLower(strings.TrimPrefix(branch, "refs/heads/"))
	b = previewSlugRe.ReplaceAllString(b, "-")
	b = strings.Trim(b, "-")
	if len(b) > 24 {
		b = strings.Trim(b[:24], "-")
	}
	return b
}

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
