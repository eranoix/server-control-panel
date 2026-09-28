package jiraai

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"server-control-panel/internal/aiprompts"
	"server-control-panel/internal/claudebin"
	"server-control-panel/internal/jira"
)

type Args struct {
	IssueKey     string `json:"issue_key"`
	Owner        string `json:"owner"`
	RepoOverride string `json:"repo_override,omitempty"`
}

type ClientFor func(owner string) (*jira.Client, error)

type RepoMapFor func(owner string) map[string]string

type Runner struct {
	clientFor     ClientFor
	repoMapFor    RepoMapFor
	prompts       *aiprompts.Registry
	claudeBin     string
	timeout       time.Duration
	verifyTimeout time.Duration
	accountDir    func() string
	model         func() string
}

func NewRunner(clientFor ClientFor, repoMapFor RepoMapFor, prompts *aiprompts.Registry, accountDir func() string, model func() string) *Runner {
	return &Runner{
		clientFor:     clientFor,
		repoMapFor:    repoMapFor,
		prompts:       prompts,
		claudeBin:     claudebin.Path(),
		timeout:       15 * time.Minute,
		verifyTimeout: verifyTimeoutDefault(),
		accountDir:    accountDir,
		model:         model,
	}
}

func (r *Runner) effectiveModel() string {
	if r.model == nil {
		return ""
	}
	return r.model()
}

func (r *Runner) claudeArgs(base ...string) []string {
	if m := r.effectiveModel(); m != "" {
		return append([]string{"--model", m}, base...)
	}
	return base
}

func (r *Runner) applyAccountEnv(cmd *exec.Cmd) {
	if r.accountDir == nil {
		return
	}
	if dir := r.accountDir(); dir != "" {
		cmd.Env = append(os.Environ(), "CLAUDE_CONFIG_DIR="+dir)
	}
}

func verifyTimeoutDefault() time.Duration {
	if v := strings.TrimSpace(os.Getenv("PANEL_AI_VERIFY_TIMEOUT")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 15 * time.Minute
}

func (r *Runner) promptRegistry() *aiprompts.Registry {
	if r.prompts != nil {
		return r.prompts
	}
	return aiprompts.New(os.TempDir())
}

func (Runner) Kind() string                        { return "jira_ai_analysis" }
func (Runner) AuthorizedFor(_ string, _ bool) bool { return true }

func (r *Runner) Run(ctx context.Context, raw json.RawMessage, logW io.Writer, progress func(int), step func(string)) error {
	var a Args
	if err := json.Unmarshal(raw, &a); err != nil {
		return fmt.Errorf("args: %w", err)
	}
	if a.IssueKey == "" || a.Owner == "" {
		return errors.New("issue_key + owner required")
	}

	cli, err := r.clientFor(a.Owner)
	if err != nil {
		return fmt.Errorf("jira client for %s: %w", a.Owner, err)
	}

	step("fetching issue " + a.IssueKey)
	fmt.Fprintf(logW, "$ jira.GetIssue %s\n", a.IssueKey)
	issue, err := cli.GetIssue(ctx, a.IssueKey)
	if err != nil {
		return fmt.Errorf("fetch issue: %w", err)
	}
	progress(5)
	projectKey := ""
	if issue.Project != nil {
		projectKey = issue.Project.Key
	}
	fmt.Fprintf(logW, "  project: %s\n  type: %s\n  status: %s\n",
		projectKey,
		nameOf(issue.IssueType),
		issue.Status.Name)

	repoPath := a.RepoOverride
	if repoPath == "" {
		m := r.repoMapFor(a.Owner)
		if m != nil {
			repoPath = m[projectKey]
		}
	}
	if repoPath == "" {
		fmt.Fprintf(logW, "  ⚠ no repo mapped for project %s — the AI will run with no cwd (generic answer)\n", projectKey)
	} else {
		fmt.Fprintf(logW, "  repo: %s\n", repoPath)
	}
	step("resolving repository")
	progress(10)

	reg := r.promptRegistry()
	originalDesc := stripAIBlock(issue.Description)
	priorBlock := extractAIBlock(issue.Description)
	priorCertainty := parsePriorCertainty(priorBlock)
	var prompt string
	if strings.TrimSpace(priorBlock) != "" {
		fmt.Fprintf(logW, "  mode: REFINEMENT (previous plan detected, prior confidence %d%%)\n", priorCertainty)
		prompt = buildRefinePrompt(issue, originalDesc, priorBlock, priorCertainty, projectKey, repoPath, reg.RefinePreamble())
	} else {
		fmt.Fprintf(logW, "  mode: AUDIT (first analysis — no previous AI plan)\n")
		prompt = buildPrompt(issue, originalDesc, projectKey, repoPath, reg.AuditPreamble())
	}
	modelNote := "opus (default)"
	if m := r.effectiveModel(); m != "" {
		modelNote = m
	}
	fmt.Fprintf(logW, "\n$ claude -p <prompt:%d chars> (model %s, timeout %s)\n", len(prompt), modelNote, r.timeout)

	ctxCmd, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctxCmd, r.claudeBin, r.claudeArgs("-p", prompt)...)
	if repoPath != "" {
		cmd.Dir = repoPath
	}
	r.applyAccountEnv(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawn claude: %w", err)
	}
	step("analyzing the repository with Claude")
	progress(20)

	var captured strings.Builder
	streamDone := make(chan struct{})
	go func() {
		s := bufio.NewScanner(stderr)
		s.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for s.Scan() {
			fmt.Fprintf(logW, "[stderr] %s\n", s.Text())
		}
	}()
	go func() {
		defer close(streamDone)
		s := bufio.NewScanner(stdout)
		s.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for s.Scan() {
			line := s.Text()
			captured.WriteString(line)
			captured.WriteByte('\n')
			fmt.Fprintln(logW, line)
		}
	}()
	if err := cmd.Wait(); err != nil {
		<-streamDone
		return fmt.Errorf("claude exit: %w", err)
	}
	<-streamDone
	step("auditing the ticket")
	progress(80)

	report := strings.TrimSpace(captured.String())
	if report == "" {
		return errors.New("claude returned empty response")
	}
	fmt.Fprintf(logW, "\n$ parse report (%d chars)\n", len(report))

	step("verifying the plan")
	verifyCtx, verifyCancel := context.WithTimeout(ctx, r.verifyTimeout)
	defer verifyCancel()
	target := ratchetThreshold(priorCertainty)
	if target != verifyAcceptThreshold {
		fmt.Fprintf(logW, "  threshold for this round: %d%% (ratchet over the previous %d%%)\n", target, priorCertainty)
	}
	verified, certainty, rounds := r.verifyPlanLoop(verifyCtx, report, repoPath, target, logW)
	if verified != "" {
		report = verified
	}
	progress(80 + certainty*15/100)
	fmt.Fprintf(logW, "\n$ verification: %d%% confidence after %d round(s)\n", certainty, rounds)
	if priorCertainty > 0 {
		fmt.Fprintf(logW, "  confidence progress: %d%% → %d%% (%+d)\n", priorCertainty, certainty, certainty-priorCertainty)
	}
	if certainty < target {
		fmt.Fprintf(logW, "  ⚠ did not reach the %d%% threshold within budget (%d round(s), up to %s) — human review recommended\n",
			target, rounds, r.verifyTimeout)
	}

	labels := parseSuggestedLabels(report)
	labels = append(labels, "ai-analyzed")
	if certainty >= target {
		labels = append(labels, "ai-verified")
	}
	newTitle := parseSuggestedTitle(report)
	fmt.Fprintf(logW, "  labels: %s\n", strings.Join(labels, ", "))
	if newTitle != "" && newTitle != issue.Summary {
		fmt.Fprintf(logW, "  suggested title: %q (was %q)\n", newTitle, issue.Summary)
	}

	stripped := stripBody(report)
	newDescription := mergeAIBlock(issue.Description, stripped)
	if clamped, did := clampForADF(newDescription, adfDescLimit); did {
		newDescription = clamped
		fmt.Fprintf(logW, "  ⚠ description exceeded %d chars — truncated in the description (the full text goes in the comment)\n", adfDescLimit)
	}

	patch := jira.UpdateIssueRequest{
		Description: &newDescription,
		Labels:      func() *[]string { m := mergeLabels(issue.Labels, labels); return &m }(),
	}
	if newTitle != "" && newTitle != issue.Summary {
		patch.Summary = &newTitle
	}
	step("updating Jira")
	fmt.Fprintf(logW, "\n$ jira.UpdateIssue (summary=%v, description=%dch, labels=%d)\n",
		patch.Summary != nil, len(newDescription), len(*patch.Labels))
	if err := cli.UpdateIssue(ctx, a.IssueKey, patch); err != nil {
		return fmt.Errorf("update issue: %w", err)
	}
	progress(90)

	commentBody := buildCommentBody(report, a.IssueKey, certainty, rounds, priorCertainty, target)
	if clamped, did := clampForADF(commentBody, adfDescLimit); did {
		commentBody = clamped
		fmt.Fprintf(logW, "  ⚠ comment exceeded %d chars — truncated\n", adfDescLimit)
	}
	fmt.Fprintf(logW, "$ jira.AddComment %s (%d chars)\n", a.IssueKey, len(commentBody))
	if _, err := cli.AddComment(ctx, a.IssueKey, commentBody); err != nil {
		fmt.Fprintf(logW, "  ⚠ comment failed (not critical, the description was already updated): %v\n", err)
	}
	step("done")
	progress(100)
	fmt.Fprintln(logW, "\n✓ analysis complete; description + labels + title updated, comment posted")
	return nil
}

const verifyAcceptThreshold = 85

const verifyMaxRoundsHardCap = 6

const verifyStagnationLimit = 2

const (
	ratchetStep = 4
	ratchetCap  = 97
)

func ratchetThreshold(priorCertainty int) int {
	t := verifyAcceptThreshold
	if priorCertainty >= t {
		t = priorCertainty + ratchetStep
	}
	if t > ratchetCap {
		t = ratchetCap
	}
	return t
}

func (r *Runner) verifyPlanLoop(ctx context.Context, initialPlan, repoPath string, threshold int, logW io.Writer) (finalPlan string, certainty, rounds int) {
	currentPlan := initialPlan
	preamble := r.promptRegistry().VerifyPreamble(threshold)
	prevCertainty := -1
	stagnant := 0
	for i := 0; i < verifyMaxRoundsHardCap; i++ {
		if ctx.Err() != nil {
			fmt.Fprintf(logW, "  ⏱ verification budget exhausted after %d round(s) — stopping at %d%%\n", rounds, certainty)
			break
		}
		rounds++
		fmt.Fprintf(logW, "\n$ verifyPlan round %d/%d (threshold=%d%%)\n", rounds, verifyMaxRoundsHardCap, threshold)
		out, err := r.runClaudeWithPrompt(ctx, repoPath, buildVerifyPrompt(preamble, currentPlan), logW)
		if err != nil {
			fmt.Fprintf(logW, "  ⚠ round failed: %v — keeping the previous round's plan\n", err)
			return currentPlan, certainty, rounds
		}
		verdict := parseVerdict(out)
		c := parseCertainty(out)
		refined := parseRefinedPlan(out)
		fmt.Fprintf(logW, "  verdict=%s confidence=%d%% refined=%dch\n", verdict, c, len(refined))
		if c > 0 {
			certainty = c
		}
		if refined != "" {
			currentPlan = wrapRefinedPlan(initialPlan, refined, verdict, c, parseRisks(out), parseSummary(out), threshold)
		}
		if verdict == "APPROVED" && c >= threshold {
			return currentPlan, certainty, rounds
		}
		if prevCertainty >= 0 && c <= prevCertainty {
			stagnant++
			if stagnant >= verifyStagnationLimit {
				fmt.Fprintf(logW, "  ⚠ confidence stuck at ~%d%% for %d rounds — stopping (never converged to the threshold)\n", c, stagnant)
				break
			}
		} else {
			stagnant = 0
		}
		prevCertainty = c
	}
	return currentPlan, certainty, rounds
}

func (r *Runner) runClaudeWithPrompt(ctx context.Context, repoPath, prompt string, logW io.Writer) (string, error) {
	cmd := exec.CommandContext(ctx, r.claudeBin, r.claudeArgs("-p", prompt)...)
	if repoPath != "" {
		cmd.Dir = repoPath
	}
	r.applyAccountEnv(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("spawn claude: %w", err)
	}
	var captured strings.Builder
	streamDone := make(chan struct{})
	go func() {
		s := bufio.NewScanner(stderr)
		s.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for s.Scan() {
			fmt.Fprintf(logW, "[stderr] %s\n", s.Text())
		}
	}()
	go func() {
		defer close(streamDone)
		s := bufio.NewScanner(stdout)
		s.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for s.Scan() {
			line := s.Text()
			captured.WriteString(line)
			captured.WriteByte('\n')
			fmt.Fprintln(logW, line)
		}
	}()
	if err := cmd.Wait(); err != nil {
		<-streamDone
		return strings.TrimSpace(captured.String()), fmt.Errorf("claude exit: %w", err)
	}
	<-streamDone
	return strings.TrimSpace(captured.String()), nil
}

func buildVerifyPrompt(assembledPreamble, plan string) string {
	var b strings.Builder
	b.WriteString(assembledPreamble)
	b.WriteString("\n\n---\nPLAN TO VERIFY:\n---\n")
	b.WriteString(plan)
	b.WriteString("\n---\n")
	return b.String()
}

var verdictRe = regexp.MustCompile(`(?im)^##\s*✅?\s*Verdict\s*\n+\s*(APPROVED|REVISE)`)

var certaintyRe = regexp.MustCompile(`(?im)^##\s*🎯?\s*Confidence\s+of\s+Success\s*\n+\s*(\d{1,3})`)

var refinedPlanRe = regexp.MustCompile(`(?ims)^##\s*📋?\s*Final\s+Plan\s*\n+(.+)$`)

var risksRe = regexp.MustCompile(`(?ims)^##\s*⚠️?\s*Risks\s*\n+(.+?)(?:\n##\s|\z)`)

var summaryRe = regexp.MustCompile(`(?ims)^##\s*🗣️?\s*In\s+short\s*\n+(.+?)(?:\n##\s|\z)`)

func parseVerdict(out string) string {
	m := verdictRe.FindStringSubmatch(out)
	if len(m) < 2 {
		return ""
	}
	return strings.ToUpper(strings.TrimSpace(m[1]))
}

func parseCertainty(out string) int {
	m := certaintyRe.FindStringSubmatch(out)
	if len(m) < 2 {
		return 0
	}
	n := 0
	for _, ch := range m[1] {
		if ch < '0' || ch > '9' {
			break
		}
		n = n*10 + int(ch-'0')
		if n > 100 {
			n = 100
		}
	}
	return n
}

func parseRefinedPlan(out string) string {
	m := refinedPlanRe.FindStringSubmatch(out)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(m[1])
}

func parseRisks(out string) string {
	m := risksRe.FindStringSubmatch(out)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(m[1])
}

func parseSummary(out string) string {
	m := summaryRe.FindStringSubmatch(out)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(m[1])
}

func wrapRefinedPlan(original, refined, verdict string, certainty int, risks, summary string, threshold int) string {
	original = stripAuditEcho(original)
	planSectionRe := regexp.MustCompile(`(?ims)(^##\s*🛠?\s*Fix\s+Plan[^\n]*\n)(.+?)(\n##\s|\z)`)
	loc := planSectionRe.FindStringSubmatchIndex(original)
	if loc == nil {
		return formatVerificationHeader(verdict, certainty, risks, summary, threshold) + "\n\n" + refined
	}
	newBody := original[:loc[2]] + refined + "\n\n" + original[loc[5]:]
	return formatVerificationHeader(verdict, certainty, risks, summary, threshold) + "\n\n" + newBody
}

func formatVerificationHeader(verdict string, certainty int, risks, summary string, threshold int) string {
	var b strings.Builder
	b.WriteString("## 🔬 Verification\n")
	b.WriteString(fmt.Sprintf("**Verdict:** %s · **Confidence:** %d%%", verdict, certainty))
	if certainty >= threshold {
		b.WriteString(" ✅\n")
	} else {
		b.WriteString(" ⚠️ (below the threshold of ")
		b.WriteString(fmt.Sprintf("%d%%)\n", threshold))
	}
	if s := strings.TrimSpace(summary); s != "" {
		b.WriteString("\n**📣 What this plan does (in short):**\n")
		b.WriteString(s)
		b.WriteString("\n")
	}
	if strings.TrimSpace(risks) != "" && !strings.EqualFold(strings.TrimSpace(risks), "None identified") {
		b.WriteString("\n**Risks identified:**\n")
		b.WriteString(risks)
		b.WriteString("\n")
	}
	return b.String()
}

func stripBody(report string) string {
	startRe := regexp.MustCompile(`(?m)^##\s*📝?\s*(?:Suggested\s+)?[Tt]itle[^\n]*\n`)
	loc := startRe.FindStringIndex(report)
	if loc == nil {
		return strings.TrimSpace(report)
	}
	rest := report[loc[1]:]
	next := sectionHeadRe.FindStringIndex(rest)
	if next == nil {
		return strings.TrimSpace(report[:loc[0]])
	}
	return strings.TrimSpace(report[:loc[0]] + rest[next[0]:])
}

var sectionHeadRe = regexp.MustCompile(`(?m)^##\s`)

var (
	auditSummaryHeadRe   = regexp.MustCompile(`(?m)^##\s*🗣️?\s*In\s+short[^\n]*\n`)
	auditCertaintyHeadRe = regexp.MustCompile(`(?m)^##\s*🎯?\s*Confidence\s+of\s+Success[^\n]*\n`)
)

func stripSection(s string, headerRe *regexp.Regexp) string {
	loc := headerRe.FindStringIndex(s)
	if loc == nil {
		return s
	}
	rest := s[loc[1]:]
	if next := sectionHeadRe.FindStringIndex(rest); next != nil {
		return s[:loc[0]] + rest[next[0]:]
	}
	return strings.TrimRight(s[:loc[0]], "\n") + "\n"
}

func stripAuditEcho(s string) string {
	s = stripSection(s, auditSummaryHeadRe)
	s = stripSection(s, auditCertaintyHeadRe)
	return s
}

const (
	aiBlockStart = "\n\n--- 🤖 AI ANALYSIS ---\n"
	aiBlockEnd   = "\n--- /🤖 AI ANALYSIS ---"
)

func mergeAIBlock(existing, aiContent string) string {
	base := stripAIBlock(existing)
	stamp := time.Now().UTC().Format("2006-01-02 15:04 UTC")
	body := fmt.Sprintf("%s_(generated on %s — pressing the button again regenerates this block; the content above is preserved)_\n\n%s%s",
		aiBlockStart, stamp, strings.TrimSpace(aiContent), aiBlockEnd)
	return strings.TrimRight(base, "\n") + body
}

func stripAIBlock(s string) string {
	i := strings.Index(s, aiBlockStart)
	if i < 0 {
		return s
	}
	j := strings.Index(s, aiBlockEnd)
	if j < 0 || j < i {
		return strings.TrimRight(s[:i], "\n")
	}
	return strings.TrimRight(s[:i]+s[j+len(aiBlockEnd):], "\n")
}

func extractAIBlock(s string) string {
	i := strings.Index(s, aiBlockStart)
	if i < 0 {
		return ""
	}
	inner := s[i+len(aiBlockStart):]
	if j := strings.Index(inner, aiBlockEnd); j >= 0 {
		inner = inner[:j]
	}
	inner = strings.TrimSpace(inner)
	inner = stampLineRe.ReplaceAllString(inner, "")
	return strings.TrimSpace(inner)
}

var stampLineRe = regexp.MustCompile(`(?m)^_\(generated on [^\n]*\)_\n?`)

var priorCertaintyRe = regexp.MustCompile(`(?i)\*\*Confidence:\*\*\s*(\d{1,3})\s*%`)

func parsePriorCertainty(block string) int {
	m := priorCertaintyRe.FindStringSubmatch(block)
	if len(m) < 2 {
		return 0
	}
	n := 0
	for _, ch := range m[1] {
		n = n*10 + int(ch-'0')
	}
	if n > 100 {
		n = 100
	}
	return n
}

func nameOf(n *jira.NamedRef) string {
	if n == nil {
		return ""
	}
	return n.Name
}

const adfDescLimit = 30000

func clampForADF(s string, limit int) (string, bool) {
	if len(s) <= limit {
		return s, false
	}
	const note = "\n\n…(truncated to fit Jira's limit)"
	cut := limit - len(note)
	if cut < 0 {
		cut = 0
	}
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return strings.TrimSpace(s[:cut]) + note, true
}

func writeIssueMeta(b *strings.Builder, d *jira.IssueDetail, projectKey, repoPath, cleanDesc string) {
	b.WriteString("**Ticket:** ")
	b.WriteString(d.Key)
	if projectKey != "" {
		b.WriteString(" (project: ")
		b.WriteString(projectKey)
		b.WriteString(")")
	}
	b.WriteString("\n")
	if d.IssueType != nil {
		b.WriteString("**Type:** ")
		b.WriteString(d.IssueType.Name)
		b.WriteString("\n")
	}
	if d.Priority != nil {
		b.WriteString("**Priority:** ")
		b.WriteString(d.Priority.Name)
		b.WriteString("\n")
	}
	if d.Status.Name != "" {
		b.WriteString("**Status:** ")
		b.WriteString(d.Status.Name)
		b.WriteString("\n")
	}
	if len(d.Labels) > 0 {
		b.WriteString("**Current labels:** ")
		b.WriteString(strings.Join(d.Labels, ", "))
		b.WriteString("\n")
	}
	if repoPath != "" {
		b.WriteString("**Repository (your cwd):** ")
		b.WriteString(repoPath)
		b.WriteString("\n")
	}
	b.WriteString("\n**Title:** ")
	b.WriteString(d.Summary)
	b.WriteString("\n\n**Original ticket description:**\n")
	if strings.TrimSpace(cleanDesc) == "" {
		b.WriteString("_(no description — use the title + project name as context)_")
	} else {
		b.WriteString(cleanDesc)
	}
	b.WriteString("\n")
}

func buildPrompt(d *jira.IssueDetail, cleanDesc, projectKey, repoPath, preamble string) string {
	var b strings.Builder
	b.WriteString(preamble)
	b.WriteString("\n\n---\n\n")
	writeIssueMeta(&b, d, projectKey, repoPath, cleanDesc)
	return b.String()
}

func buildRefinePrompt(d *jira.IssueDetail, cleanDesc, priorPlan string, priorCertainty int, projectKey, repoPath, preamble string) string {
	var b strings.Builder
	b.WriteString(preamble)
	b.WriteString("\n\n---\n\n")
	writeIssueMeta(&b, d, projectKey, repoPath, cleanDesc)
	b.WriteString("\n---\nPREVIOUS PLAN ")
	if priorCertainty > 0 {
		fmt.Fprintf(&b, "(confidence measured in the last round: %d%%) ", priorCertainty)
	}
	b.WriteString("— your job is to make it demonstrably stronger and raise that confidence:\n---\n")
	b.WriteString(priorPlan)
	b.WriteString("\n---\n\n")
	b.WriteString("Deliver a plan STRICTLY better than the one above: confirm every assumption in the code, resolve the risks/uncertainties that held the confidence down, deepen the vague steps and add concrete verification. Do not repeat the previous plan — beat it.\n")
	return b.String()
}

func buildCommentBody(report, issueKey string, certainty, rounds, priorCertainty, threshold int) string {
	stamp := time.Now().UTC().Format("2006-01-02 15:04 UTC")
	verdict := "⚠️ human review recommended"
	if certainty >= threshold {
		verdict = "✅ approved automatically"
	}
	progress := ""
	if priorCertainty > 0 {
		progress = fmt.Sprintf(" · progress %d%% → %d%% (%+d)", priorCertainty, certainty, certainty-priorCertainty)
	}
	header := fmt.Sprintf(
		"🤖 *Automated analysis (server-control-panel AI · %s)*\n\n"+
			"Generated from the title + description of ticket %s.\n\n"+
			"**Iterative verification:** %s · confidence %d%% (target %d%%) · %d round(s)%s\n",
		stamp, issueKey, verdict, certainty, threshold, rounds, progress,
	)
	return header + "\n---\n\n" + report
}

var titleLineRe = regexp.MustCompile(`(?m)^##\s*📝?\s*(?:Suggested\s+)?[Tt]itle[^\n]*\n+([^\n]+)`)

func parseSuggestedTitle(report string) string {
	m := titleLineRe.FindStringSubmatch(report)
	if len(m) < 2 {
		return ""
	}
	t := strings.TrimSpace(m[1])
	t = strings.Trim(t, "`\"'*_")
	t = strings.TrimSpace(t)
	if len(t) > 255 {
		t = t[:255]
	}
	return t
}

var labelLineRe = regexp.MustCompile(`(?m)^##\s*🏷?\s*Labels[^\n]*\n+([^\n]+)`)

func parseSuggestedLabels(report string) []string {
	m := labelLineRe.FindStringSubmatch(report)
	if len(m) < 2 {
		return nil
	}
	raw := strings.Split(m[1], ",")
	seen := map[string]bool{}
	out := []string{}
	for _, t := range raw {
		s := strings.TrimSpace(t)
		s = strings.Trim(s, "`*_-")
		s = strings.ToLower(s)
		s = strings.ReplaceAll(s, " ", "-")
		if s == "" || len(s) > 60 {
			continue
		}
		valid := true
		for _, r := range s {
			ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_'
			if !ok {
				valid = false
				break
			}
		}
		if valid && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func mergeLabels(existing, additions []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(existing)+len(additions))
	for _, l := range existing {
		if l != "" && !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	for _, l := range additions {
		if l != "" && !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	return out
}

func labelsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
