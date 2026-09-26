package main

// session_tokens.go — per-session / per-day token + cost totals from Claude
// Code JSONL transcripts.
//
//	vpsmctl session-tokens --session <uuid> [--home DIR] [--json]
//	vpsmctl session-tokens --cwd /opt/panel [--json]     # newest in project
//	vpsmctl session-tokens --project -opt-panel [--json]
//	vpsmctl session-tokens --all [--home DIR] [--json]          # every project
//
// Reuses the JSONL location + parsing helpers from session_transcript.go
// (projectMangle, newestJSONL, transcriptLine, msgEnvelope, usageBlock).

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// priceRow / cliModelPriceTable mirror internal/api/agent_cost.go's authoritative
// table (per MTok: in / out / cache-write(1.25×in) / cache-read(0.1×in)).
type priceRow struct{ in, out, cacheWrite, cacheRead float64 }

var cliModelPriceTable = []struct {
	match string
	price priceRow
}{
	{"opus-4-8", priceRow{5, 25, 6.25, 0.50}},
	{"opus-4-7", priceRow{5, 25, 6.25, 0.50}},
	{"opus-4-6", priceRow{5, 25, 6.25, 0.50}},
	{"opus-4-5", priceRow{5, 25, 6.25, 0.50}},
	{"opus-4-1", priceRow{15, 75, 18.75, 1.50}},
	{"opus-4", priceRow{15, 75, 18.75, 1.50}},
	{"opus-3", priceRow{15, 75, 18.75, 1.50}},
	{"sonnet", priceRow{3, 15, 3.75, 0.30}},
	{"haiku", priceRow{1, 5, 1.25, 0.10}},
}

var cliDefaultPrice = priceRow{5, 25, 6.25, 0.50}

func cliPriceFor(model string) priceRow {
	m := strings.ToLower(model)
	for _, row := range cliModelPriceTable {
		if strings.Contains(m, row.match) {
			return row.price
		}
	}
	return cliDefaultPrice
}

// dayBucket accumulates per-model tokens for one (session, day) key.
type dayBucket struct {
	Session string  `json:"session"`
	Day     string  `json:"day"`
	In      int64   `json:"in"`
	Out     int64   `json:"out"`
	CacheR  int64   `json:"cache_read"`
	CacheW  int64   `json:"cache_creation"`
	Cost    float64 `json:"cost_usd"`
	// per-model token sub-totals feed the cost; not emitted.
	perModel map[string]*usageBlock
}

func cmdSessionTokens(args []string) error {
	fs := flag.NewFlagSet("session-tokens", flag.ContinueOnError)
	session := fs.String("session", "", "session id (JSONL basename)")
	cwd := fs.String("cwd", "", "session working dir (mapped to project dir)")
	project := fs.String("project", "", "explicit mangled project dir name")
	home := fs.String("home", "/root/.claude", "Claude home holding projects/")
	user := fs.String("user", "", "user label (header only)")
	all := fs.Bool("all", false, "scan every JSONL under all projects")
	asJSON := fs.Bool("json", false, "emit JSON instead of a table")
	if err := fs.Parse(args); err != nil {
		return err
	}

	paths, err := collectJSONL(*home, *session, *cwd, *project, *all)
	if err != nil {
		return err
	}

	// Aggregate into (session|day) buckets.
	buckets := map[string]*dayBucket{}
	for _, p := range paths {
		if err := accumulate(p, buckets); err != nil {
			fmt.Fprintf(os.Stderr, "warn: %s: %v\n", p, err)
		}
	}
	// Finalize cost per bucket.
	rows := make([]*dayBucket, 0, len(buckets))
	for _, b := range buckets {
		for model, tok := range b.perModel {
			pr := cliPriceFor(model)
			b.Cost += float64(tok.InputTokens)/1e6*pr.in +
				float64(tok.OutputTokens)/1e6*pr.out +
				float64(tok.CacheCreationInputTokens)/1e6*pr.cacheWrite +
				float64(tok.CacheReadInputTokens)/1e6*pr.cacheRead
		}
		rows = append(rows, b)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Session != rows[j].Session {
			return rows[i].Session < rows[j].Session
		}
		return rows[i].Day < rows[j].Day
	})

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{"user": *user, "rows": rows})
	}

	if *user != "" {
		fmt.Printf("user: %s\n", *user)
	}
	fmt.Printf("%-40s %-10s %12s %12s %12s %12s %10s\n",
		"session", "day", "in", "out", "cache_r", "cache_w", "cost_usd")
	var gIn, gOut, gCR, gCW int64
	var gCost float64
	for _, b := range rows {
		fmt.Printf("%-40s %-10s %12d %12d %12d %12d %10.4f\n",
			trim40(b.Session), b.Day, b.In, b.Out, b.CacheR, b.CacheW, b.Cost)
		gIn += b.In
		gOut += b.Out
		gCR += b.CacheR
		gCW += b.CacheW
		gCost += b.Cost
	}
	fmt.Printf("%-40s %-10s %12d %12d %12d %12d %10.4f\n",
		"TOTAL", "", gIn, gOut, gCR, gCW, gCost)
	return nil
}

func trim40(s string) string {
	if len(s) > 40 {
		return s[:37] + "..."
	}
	return s
}

// collectJSONL resolves the set of JSONL files to aggregate.
func collectJSONL(home, session, cwd, project string, all bool) ([]string, error) {
	projectsDir := filepath.Join(home, "projects")
	if all {
		var out []string
		dirs, err := os.ReadDir(projectsDir)
		if err != nil {
			return nil, fmt.Errorf("projects dir: %w", err)
		}
		for _, d := range dirs {
			if !d.IsDir() {
				continue
			}
			files, _ := os.ReadDir(filepath.Join(projectsDir, d.Name()))
			for _, f := range files {
				if !f.IsDir() && strings.HasSuffix(f.Name(), ".jsonl") {
					out = append(out, filepath.Join(projectsDir, d.Name(), f.Name()))
				}
			}
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("no .jsonl under %s", projectsDir)
		}
		return out, nil
	}
	// Single transcript via the shared locator.
	p, err := locateTranscript(home, session, cwd, project)
	if err != nil {
		return nil, err
	}
	return []string{p}, nil
}

// accumulate reads one JSONL into (session|day) buckets, summing usage per model.
func accumulate(path string, buckets map[string]*dayBucket) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	fileSession := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var tl transcriptLine
		if err := json.Unmarshal(line, &tl); err != nil {
			continue
		}
		if tl.Type != "assistant" {
			continue
		}
		var env msgEnvelope
		if err := json.Unmarshal(tl.Message, &env); err != nil || env.Usage == nil {
			continue
		}
		day := "unknown"
		if len(tl.Timestamp) >= 10 {
			day = tl.Timestamp[:10]
		}
		sess := fileSession
		if tl.SessionID != "" {
			sess = tl.SessionID
		}
		key := sess + "|" + day
		b := buckets[key]
		if b == nil {
			b = &dayBucket{Session: sess, Day: day, perModel: map[string]*usageBlock{}}
			buckets[key] = b
		}
		u := env.Usage
		b.In += int64(u.InputTokens)
		b.Out += int64(u.OutputTokens)
		b.CacheR += int64(u.CacheReadInputTokens)
		b.CacheW += int64(u.CacheCreationInputTokens)
		pm := b.perModel[env.Model]
		if pm == nil {
			pm = &usageBlock{}
			b.perModel[env.Model] = pm
		}
		pm.InputTokens += u.InputTokens
		pm.OutputTokens += u.OutputTokens
		pm.CacheReadInputTokens += u.CacheReadInputTokens
		pm.CacheCreationInputTokens += u.CacheCreationInputTokens
	}
	return sc.Err()
}
