package main

// session_transcript.go — export a Claude Code session transcript (JSONL) to
// readable markdown, keyed to a ticket/session.
//
// Claude Code writes one JSONL per session under
// <claude-home>/projects/<mangled-cwd>/<session-uuid>.jsonl, where the file
// basename IS the session id (and the mangled dir is the cwd with every
// non-alphanumeric char replaced by '-'). This subcommand locates the right
// JSONL — by session id, by cwd (newest in that project), or by an explicit
// project dir — and renders the user/assistant turns to markdown.
//
//   vpsmctl session-transcript --session <uuid> [--home DIR] [--out FILE]
//   vpsmctl session-transcript --cwd /opt/panel [--out FILE]   # newest
//   vpsmctl session-transcript --project -opt-panel [--out FILE]
//
// Flags:
//   --session  session id (JSONL basename, with or without .jsonl)
//   --cwd      working dir of the session; mapped to the project dir
//   --project  explicit mangled project dir name (escape hatch)
//   --home     Claude home holding projects/ (default /root/.claude)
//   --user     label only (recorded in the header)
//   --out      output file; default: stdout
//   --thinking include assistant "thinking" blocks
//   --tools    include tool_use / tool_result blocks

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

func cmdSessionTranscript(args []string) error {
	fs := flag.NewFlagSet("session-transcript", flag.ContinueOnError)
	session := fs.String("session", "", "session id (JSONL basename)")
	cwd := fs.String("cwd", "", "session working dir (mapped to project dir)")
	project := fs.String("project", "", "explicit mangled project dir name")
	home := fs.String("home", "/root/.claude", "Claude home holding projects/")
	user := fs.String("user", "", "user label (header only)")
	out := fs.String("out", "", "output file (default stdout)")
	thinking := fs.Bool("thinking", false, "include assistant thinking blocks")
	tools := fs.Bool("tools", false, "include tool_use/tool_result blocks")
	if err := fs.Parse(args); err != nil {
		return err
	}

	jsonlPath, err := locateTranscript(*home, *session, *cwd, *project)
	if err != nil {
		return err
	}

	md, err := renderTranscript(jsonlPath, *user, *thinking, *tools)
	if err != nil {
		return err
	}

	if *out == "" {
		fmt.Print(md)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(*out, []byte(md), 0o600); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %s (%d bytes) from %s\n", *out, len(md), jsonlPath)
	return nil
}

// projectMangle mirrors Claude Code's cwd->project-dir mapping: every
// non-alphanumeric character becomes '-'.
var nonAlnumRe = regexp.MustCompile(`[^a-zA-Z0-9]`)

func projectMangle(cwd string) string {
	return nonAlnumRe.ReplaceAllString(cwd, "-")
}

// locateTranscript resolves the JSONL path from the given selectors, in
// priority order: explicit session id -> cwd (newest) -> project dir (newest).
func locateTranscript(home, session, cwd, project string) (string, error) {
	projectsDir := filepath.Join(home, "projects")
	if fi, err := os.Stat(projectsDir); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("projects dir not found: %s", projectsDir)
	}

	if session != "" {
		base := strings.TrimSuffix(session, ".jsonl") + ".jsonl"
		// Search a specific project first if provided, else all projects.
		var globs []string
		if project != "" {
			globs = []string{filepath.Join(projectsDir, project, base)}
		} else if cwd != "" {
			globs = []string{filepath.Join(projectsDir, projectMangle(cwd), base)}
		} else {
			globs = []string{filepath.Join(projectsDir, "*", base)}
		}
		for _, g := range globs {
			matches, _ := filepath.Glob(g)
			if len(matches) > 0 {
				return matches[0], nil
			}
		}
		return "", fmt.Errorf("no JSONL for session %q under %s", session, projectsDir)
	}

	// No session id: pick the newest JSONL in the resolved project dir.
	var dir string
	switch {
	case cwd != "":
		dir = filepath.Join(projectsDir, projectMangle(cwd))
	case project != "":
		dir = filepath.Join(projectsDir, project)
	default:
		return "", fmt.Errorf("provide --session, --cwd or --project")
	}
	return newestJSONL(dir)
}

func newestJSONL(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("unreadable project dir %s: %w", dir, err)
	}
	type cand struct {
		path string
		mod  int64
	}
	var cands []cand
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		cands = append(cands, cand{filepath.Join(dir, e.Name()), info.ModTime().UnixNano()})
	}
	if len(cands) == 0 {
		return "", fmt.Errorf("no .jsonl in %s", dir)
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].mod > cands[j].mod })
	return cands[0].path, nil
}

// --- JSONL rendering ---

type transcriptLine struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	Message   json.RawMessage `json:"message"`
	CWD       string          `json:"cwd,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Version   string          `json:"version,omitempty"`
}

type msgEnvelope struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
	Model   string          `json:"model,omitempty"`
	Usage   *usageBlock     `json:"usage,omitempty"`
}

type usageBlock struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

type contentBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	Thinking string          `json:"thinking,omitempty"`
	Name     string          `json:"name,omitempty"`
	Input    json.RawMessage `json:"input,omitempty"`
	Content  json.RawMessage `json:"content,omitempty"` // tool_result payload
}

func renderTranscript(path, user string, thinking, tools bool) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	var b strings.Builder
	var meta transcriptLine
	var inTok, outTok, cacheRead, cacheCreate, turns int

	body := &strings.Builder{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var tl transcriptLine
		if err := json.Unmarshal(line, &tl); err != nil {
			continue // skip queue-operation / malformed lines
		}
		if tl.CWD != "" && meta.CWD == "" {
			meta = tl
		}
		if tl.Type != "user" && tl.Type != "assistant" {
			continue
		}
		var env msgEnvelope
		if err := json.Unmarshal(tl.Message, &env); err != nil {
			continue
		}
		text := renderBlocks(env, thinking, tools)
		if strings.TrimSpace(text) == "" {
			continue
		}
		turns++
		heading := "User"
		if tl.Type == "assistant" {
			heading = "Assistant"
		}
		fmt.Fprintf(body, "## %s", heading)
		if tl.Timestamp != "" {
			fmt.Fprintf(body, "  \n*%s*", tl.Timestamp)
		}
		body.WriteString("\n\n")
		body.WriteString(text)
		body.WriteString("\n\n---\n\n")
		if env.Usage != nil {
			inTok += env.Usage.InputTokens
			outTok += env.Usage.OutputTokens
			cacheRead += env.Usage.CacheReadInputTokens
			cacheCreate += env.Usage.CacheCreationInputTokens
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}

	// Header + token summary (usable for cost/token reporting).
	fmt.Fprintf(&b, "# Transcript — %s\n\n", filepath.Base(path))
	if user != "" {
		fmt.Fprintf(&b, "- **user**: %s\n", user)
	}
	if meta.CWD != "" {
		fmt.Fprintf(&b, "- **cwd**: %s\n", meta.CWD)
	}
	if meta.SessionID != "" {
		fmt.Fprintf(&b, "- **session**: %s\n", meta.SessionID)
	}
	if meta.Version != "" {
		fmt.Fprintf(&b, "- **claude version**: %s\n", meta.Version)
	}
	fmt.Fprintf(&b, "- **turns**: %d\n", turns)
	fmt.Fprintf(&b, "- **tokens**: in=%d out=%d cache_read=%d cache_write=%d\n\n", inTok, outTok, cacheRead, cacheCreate)
	b.WriteString("---\n\n")
	b.WriteString(body.String())
	return b.String(), nil
}

// renderBlocks turns a message's content (string or block array) into markdown.
func renderBlocks(env msgEnvelope, thinking, tools bool) string {
	// content may be a plain string (typical for simple user turns).
	var asString string
	if err := json.Unmarshal(env.Content, &asString); err == nil {
		return asString
	}
	var blocks []contentBlock
	if err := json.Unmarshal(env.Content, &blocks); err != nil {
		return ""
	}
	var parts []string
	for _, bl := range blocks {
		switch bl.Type {
		case "text":
			if strings.TrimSpace(bl.Text) != "" {
				parts = append(parts, bl.Text)
			}
		case "thinking":
			if thinking && strings.TrimSpace(bl.Thinking) != "" {
				parts = append(parts, "> *(thinking)*\n>\n> "+strings.ReplaceAll(bl.Thinking, "\n", "\n> "))
			}
		case "tool_use":
			if tools {
				parts = append(parts, fmt.Sprintf("```tool_use: %s\n%s\n```", bl.Name, string(bl.Input)))
			}
		case "tool_result":
			if tools {
				parts = append(parts, "```tool_result\n"+toolResultText(bl.Content)+"\n```")
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

// toolResultText flattens a tool_result content (string or block array) to text.
func toolResultText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var out []string
		for _, bl := range blocks {
			if bl.Text != "" {
				out = append(out, bl.Text)
			}
		}
		return strings.Join(out, "\n")
	}
	return string(raw)
}
