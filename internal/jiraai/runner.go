// Package jiraai is the "Iniciar AI" feature: given a Jira issue key, spawn
// the local `claude` CLI inside the project's repository, capture the audit
// report, post it as a comment, and tag the issue with labels.
//
// Architecture overview:
//
//	handler (HTTP)                       queue worker                   this runner
//	POST /api/jira/issue/{key}/ai-analyze
//	    │                                                                   │
//	    └─→ enqueue(kind=jira_ai_analysis, args={IssueKey,Owner}) ─→ Run()
//	                                                                       │
//	                                                                       ├─ fetch issue (per-owner Jira client)
//	                                                                       ├─ resolve repo path (per-owner vault map)
//	                                                                       ├─ spawn `claude -p <prompt>` cwd=repo
//	                                                                       ├─ stream stdout to queue logW
//	                                                                       ├─ parse markdown sections
//	                                                                       ├─ POST comment back to Jira
//	                                                                       └─ PUT labels (ai-analyzed + parsed labels)
//
// Per-user injection: the runner accepts factory funcs at construction so
// each job runs with its owner's Jira credentials and repo mapping.
//
// Project → repo mapping lives in the user's vault under `jira_project_repos`
// as JSON `{"TTW":"/root/projetos/northwind-web","CSS":"/root/projetos/acme-booking",...}`.
// Empty mapping = the AI runs without a cwd (generic advice only).
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

// Args is the payload the handler sends to the queue.
type Args struct {
	IssueKey     string `json:"issue_key"`
	Owner        string `json:"owner"`
	RepoOverride string `json:"repo_override,omitempty"` // optional caller override
}

// ClientFor returns a configured Jira client for the given owner, or an
// error if the user has no vault config. Wired by NewRunner from
// handlers_jira.go's existing helper.
type ClientFor func(owner string) (*jira.Client, error)

// RepoMapFor returns the project_key → repo_path map for the given owner.
// Empty map is OK (AI runs without cwd). Wired by NewRunner.
type RepoMapFor func(owner string) map[string]string

// Runner is the queue.Runner implementation.
type Runner struct {
	clientFor  ClientFor
	repoMapFor RepoMapFor
	prompts    *aiprompts.Registry // runtime-editable prompt templates
	claudeBin  string              // path to the claude CLI (absolute; see internal/claudebin)
	timeout    time.Duration       // hard wall-clock cap per audit (default 15min)
	// verifyTimeout is the convergence loop's OWN wall-clock budget,
	// independent of the main audit's `timeout`. Derived from the parent
	// ctx (the queue imposes no deadline — see queue.go), so the verify loop
	// can keep refining toward the threshold without eating into (or being
	// starved by) the time the initial audit already spent. Env override:
	// VPSM_AI_VERIFY_TIMEOUT (a Go duration like "15m").
	verifyTimeout time.Duration
	// accountDir returns the CLAUDE_CONFIG_DIR for the "jobs" consumer,
	// or "" to inherit the process default ($HOME/.claude). Read fresh per
	// spawn so a runtime account reassignment takes effect on the next job
	// without restarting vps-manager. nil (tests/degraded boot) → inherit,
	// i.e. byte-identical to the behaviour before per-consumer accounts.
	accountDir func() string
	// model returns the --model to pass to `claude` for this audit/refine
	// tier, or "" to inherit the process default (Opus). Read fresh per spawn
	// (same contract as accountDir) so a runtime config/UI change takes effect
	// on the next job. nil (tests/degraded boot) → inherit, i.e. byte-identical
	// to the era before per-tier models: the spawn stays `claude -p <prompt>`
	// with no --model. Default policy keeps this "" so deep ticket reasoning
	// stays on Opus unless explicitly rerouted via env/config.
	model func() string
}

// NewRunner wires the per-owner Jira client + repo map factories and the
// shared prompt registry. The registry may be nil in tests/degraded boot;
// promptsOrDefault falls back to compiled-in defaults so the runner always
// has usable prompts.
//
// accountDir resolves the CLAUDE_CONFIG_DIR for jobs at spawn time; pass
// nil to keep the default account (inherit $HOME/.claude).
//
// model resolves the `--model` for the jira_ai tier at spawn time; pass nil
// (or a func returning "") to inherit the process default (Opus), keeping
// the spawn byte-identical to the era before per-tier models.
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

// effectiveModel returns the --model to pass for this spawn, or "" to inherit
// the process default (Opus). nil resolver → "" (the byte-identical default).
func (r *Runner) effectiveModel() string {
	if r.model == nil {
		return ""
	}
	return r.model()
}

// claudeArgs prefixes `--model <m>` to base only when a tier model is
// configured. An empty model returns base unchanged so the spawn stays exactly
// `claude -p <prompt>` — no implicit `--model opus`, no regression on the
// default path.
func (r *Runner) claudeArgs(base ...string) []string {
	if m := r.effectiveModel(); m != "" {
		return append([]string{"--model", m}, base...)
	}
	return base
}

// applyAccountEnv injects CLAUDE_CONFIG_DIR for the assigned jobs account.
// An empty dir (default account "jordan") leaves cmd.Env nil, so the spawn
// inherits os.Environ() exactly as it did before this feature existed —
// no regression on the default path. The router (ANTHROPIC_BASE_URL in the
// shared settings.json) is orthogonal and untouched.
func (r *Runner) applyAccountEnv(cmd *exec.Cmd) {
	if r.accountDir == nil {
		return
	}
	if dir := r.accountDir(); dir != "" {
		cmd.Env = append(os.Environ(), "CLAUDE_CONFIG_DIR="+dir)
	}
}

// verifyTimeoutDefault reads VPSM_AI_VERIFY_TIMEOUT or falls back to 15min.
// 15min matches the "hard cap of 6 rounds + a time budget" convergence policy:
// enough wall-clock for ~6 refinement rounds without letting a single vague
// ticket starve the 3-worker pool indefinitely.
func verifyTimeoutDefault() time.Duration {
	if v := strings.TrimSpace(os.Getenv("VPSM_AI_VERIFY_TIMEOUT")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 15 * time.Minute
}

// promptRegistry returns r.prompts or a throwaway default registry so the
// runner never nil-panics when the registry failed to init.
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

	// ── 1. Fetch issue ────────────────────────────────────────────────
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

	// ── 2. Resolve repo path ──────────────────────────────────────────
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

	// ── 3. Detect a prior AI plan → pick AUDIT (cold) vs REFINE (warm) ─
	// On a re-run the issue already carries the previous analysis block in
	// its description. We split it OUT of the original ticket text so the
	// model never confuses its own prior output with the user's ask, then
	// feed it back explicitly as "harden THIS plan and beat its certainty".
	// This is the lever that makes each run improve the plan instead of
	// paraphrasing it from a cold start.
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

	// ── 4. Spawn claude in cwd ────────────────────────────────────────
	ctxCmd, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctxCmd, r.claudeBin, r.claudeArgs("-p", prompt)...)
	if repoPath != "" {
		cmd.Dir = repoPath
	}
	// claude needs HOME to read its ~/.claude config — preserve the parent env.
	// applyAccountEnv may override CLAUDE_CONFIG_DIR (the jobs account).
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

	// Stream stdout + capture full output in parallel.
	var captured strings.Builder
	streamDone := make(chan struct{})
	go func() {
		// stderr to log only (not captured into result)
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

	// ── 5. Iterative verification loop ───────────────────────────────
	// The initial plan may be shallow or rest on wrong assumptions
	// (file:line doesn't match, a side effect ignored). We run Claude
	// again as a REVIEWER — it simulates applying the plan, reads the
	// files to confirm assumptions, and gives a 0-100% certainty.
	// It keeps refining until it hits the threshold or the round cap.
	step("verifying the plan")
	// The loop gets its OWN budget, derived from the PARENT ctx (not from
	// ctxCmd, which already spent part of the 15min on the main audit). The
	// queue imposes no deadline, so this WithTimeout is the loop's wall-clock cap.
	verifyCtx, verifyCancel := context.WithTimeout(ctx, r.verifyTimeout)
	defer verifyCancel()
	// Ratcheted threshold: on a first analysis it is the floor (85%); on a
	// re-run the bar rises above the previous certainty, forcing the loop to
	// deliver a plan provably stronger than the last round's — this is what
	// makes certainty "improve every time it runs" instead of plateauing.
	target := ratchetThreshold(priorCertainty)
	if target != verifyAcceptThreshold {
		fmt.Fprintf(logW, "  threshold for this round: %d%% (ratchet over the previous %d%%)\n", target, priorCertainty)
	}
	verified, certainty, rounds := r.verifyPlanLoop(verifyCtx, report, repoPath, target, logW)
	if verified != "" {
		report = verified
	}
	// progress mapped to real certainty (it used to be a hardcoded 85): 80% =
	// audit done; the rest of the bar reflects how close to the threshold we got.
	progress(80 + certainty*15/100)
	fmt.Fprintf(logW, "\n$ verification: %d%% confidence after %d round(s)\n", certainty, rounds)
	if priorCertainty > 0 {
		fmt.Fprintf(logW, "  confidence progress: %d%% → %d%% (%+d)\n", priorCertainty, certainty, certainty-priorCertainty)
	}
	if certainty < target {
		fmt.Fprintf(logW, "  ⚠ did not reach the %d%% threshold within budget (%d round(s), up to %s) — human review recommended\n",
			target, rounds, r.verifyTimeout)
	}

	// ── 6. Parse AI suggestions ───────────────────────────────────────
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

	// ── 7. Build new description with AI block (idempotent) ───────────
	// Append the report to the existing description under a unique marker.
	// Re-runs replace only the previous block — the original description is
	// preserved and does not grow with every analysis.
	stripped := stripBody(report)
	newDescription := mergeAIBlock(issue.Description, stripped)
	// Jira rejects ADF descriptions above ~32KB. Clamp so UpdateIssue doesn't
	// 400 — the comment (best-effort, further below) carries the full text
	// when it fits. Truncates the tail (= the AI block), preserving the
	// original text.
	if clamped, did := clampForADF(newDescription, adfDescLimit); did {
		newDescription = clamped
		fmt.Fprintf(logW, "  ⚠ description exceeded %d chars — truncated in the description (the full text goes in the comment)\n", adfDescLimit)
	}

	// ── 8. Single UpdateIssue with summary + description + labels ─────
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

	// ── 9. Post comment too (audit trail — re-runs leave history) ─────
	// NOTE: AddComment APPENDS — it is NOT idempotent, unlike UpdateIssue/
	// mergeAIBlock/stripAIBlock/mergeLabels which REPLACE the AI block. This
	// asymmetry is exactly why jira_ai_analysis is excluded from
	// queue.SafeToResume: an automatic post-restart rerun would post a
	// DUPLICATE comment (and double the Claude cost). The user re-runs an
	// interrupted analysis deliberately (refinement mode), so the boot
	// reconcile marks it "interrupted" instead of silently resuming it.
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

// verifyAcceptThreshold is the minimum certainty for accepting a plan as
// ready. Below that, we refine. 85% balances rigour against runtime
// (each round costs ~60s of Claude).
const verifyAcceptThreshold = 85

// verifyMaxRoundsHardCap is the absolute ceiling on refinement rounds.
// The ticket asked for "refine until ≥85%, however many rounds it takes".
// A literally infinite loop is unsafe with 3 shared workers (starvation),
// so convergence is bounded by THREE brakes:
//  1. reaching the threshold (APROVADO && certainty >= verifyAcceptThreshold);
//  2. this hard cap on rounds;
//  3. its own time budget (Runner.verifyTimeout);
//
// plus the anti-stagnation rule below. 6 leaves real room to converge before
// giving up and asking for a human review.
const verifyMaxRoundsHardCap = 6

// verifyStagnationLimit stops the loop when certainty has not improved for N
// consecutive rounds — there is no point burning budget once the model has
// plateaued below the threshold.
const verifyStagnationLimit = 2

// Certainty ratchet for re-runs. When the ticket already carries a plan whose
// previous certainty is >= the floor, this round's bar rises ratchetStep points
// above the last one (up to ratchetCap) — the loop only "approves" if it
// delivers a plan provably stronger than the previous one. This operationalises
// the request "certainty improves every time it runs": each analysis has to beat
// the last, not merely repeat 85%. The cap avoids chasing 100% (unreachable in
// practice — there is always residual uncertainty) and burning budget for nothing.
const (
	ratchetStep = 4
	ratchetCap  = 97
)

// ratchetThreshold returns this round's acceptance bar given the certainty
// measured in the previous round (0 = no prior plan). It is always >= the
// verifyAcceptThreshold floor; on a re-run above the floor, it steps up.
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

// verifyPlanLoop runs the review cycle until it reaches the threshold or the
// round cap. Returns the best plan reached + the final certainty + the number
// of rounds executed.
//
// Each round:
//  1. Build the verification prompt around the current plan
//  2. Spawn `claude -p` in the repo
//  3. Parse verdict/certainty/refined plan
//  4. If APROVADO and certainty >= threshold → stop
//  5. Otherwise, next iteration with the refined plan
//
// Best-effort on error: if a round fails (claude crashed, parse failed), it
// returns the previous round's plan instead of propagating the error. The user
// still gets a usable result.
func (r *Runner) verifyPlanLoop(ctx context.Context, initialPlan, repoPath string, threshold int, logW io.Writer) (finalPlan string, certainty, rounds int) {
	currentPlan := initialPlan
	preamble := r.promptRegistry().VerifyPreamble(threshold)
	prevCertainty := -1 // certainty of the previous round (-1 = there hasn't been one yet)
	stagnant := 0       // consecutive rounds without improvement
	for i := 0; i < verifyMaxRoundsHardCap; i++ {
		// Its own time budget: stop before an expensive new round if the
		// ctx (WithTimeout in the caller) has already expired.
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
			// The block holding the refined plan becomes the "current plan" —
			// on the next round (if any) we review the improved version.
			currentPlan = wrapRefinedPlan(initialPlan, refined, verdict, c, parseRisks(out), parseSummary(out), threshold)
		}
		if verdict == "APROVADO" && c >= threshold {
			return currentPlan, certainty, rounds
		}
		// Anti-stagnation: if certainty did not rise relative to the previous
		// round, count it; on hitting the limit, give up (it won't converge).
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

// runClaudeWithPrompt is the raw `claude -p <prompt>` invocation — used by
// verifyPlanLoop for each review round. It streams stdout into logW just like
// the main spawn, but returns the capture as a string instead of writing into
// `captured`.
//
// Timeout: it uses the caller's ctx (verifyPlanLoop passes a ctx carrying the
// verification budget, Runner.verifyTimeout).
func (r *Runner) runClaudeWithPrompt(ctx context.Context, repoPath, prompt string, logW io.Writer) (string, error) {
	cmd := exec.CommandContext(ctx, r.claudeBin, r.claudeArgs("-p", prompt)...)
	if repoPath != "" {
		cmd.Dir = repoPath
	}
	// Same jobs account inside the verification loop.
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

// buildVerifyPrompt assembles the REVIEWER prompt: the preamble put together
// by the registry (editable brain + locked output contract, already carrying the
// injected threshold) followed by the plan to verify between delimiters. The
// delimiter stays in Go — only the preamble is editable at runtime.
func buildVerifyPrompt(assembledPreamble, plan string) string {
	var b strings.Builder
	b.WriteString(assembledPreamble)
	b.WriteString("\n\n---\nPLAN TO VERIFY:\n---\n")
	b.WriteString(plan)
	b.WriteString("\n---\n")
	return b.String()
}

// verdictRe captures "APROVADO" or "REVISAR" in the "## ✅ Veredicto" block.
var verdictRe = regexp.MustCompile(`(?im)^##\s*✅?\s*Veredicto\s*\n+\s*(APROVADO|REVISAR)`)

// certaintyRe captures the 0-100 integer in the "## 🎯 Certeza de Sucesso" block.
// It tolerates variations like "85" or "85%" or "85 (alta)".
var certaintyRe = regexp.MustCompile(`(?im)^##\s*🎯?\s*Certeza\s+de\s+Sucesso\s*\n+\s*(\d{1,3})`)

// refinedPlanRe captures everything from "## 📋 Plano Final" to EOF.
var refinedPlanRe = regexp.MustCompile(`(?ims)^##\s*📋?\s*Plano\s+Final\s*\n+(.+)$`)

// risksRe captures the body of the "## ⚠️ Riscos" block up to the next "## ".
var risksRe = regexp.MustCompile(`(?ims)^##\s*⚠️?\s*Riscos\s*\n+(.+?)(?:\n##\s|\z)`)

// summaryRe captures the plain-language summary in the "## 🗣 Em resumo" block
// up to the next "## ". It is the non-technical text explaining what the plan does.
var summaryRe = regexp.MustCompile(`(?ims)^##\s*🗣️?\s*Em\s+resumo\s*\n+(.+?)(?:\n##\s|\z)`)

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

// parseSummary extracts the plain-language summary ("## 🗣 Em resumo") —
// the non-technical explanation of what the plan does, shown above the risks.
func parseSummary(out string) string {
	m := summaryRe.FindStringSubmatch(out)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(m[1])
}

// wrapRefinedPlan rewrites the plan by combining the suggested title + labels
// from the original round (which a review does not change) with the refined
// plan produced by the reviewer + the risks it identified.
//
// The final structure preserves the 6 sections the parser expects (title,
// diagnosis, audit, plan, improvements, labels) — we only replace the content
// of the "🛠 Plano de Correção" section with the refined plan.
func wrapRefinedPlan(original, refined, verdict string, certainty int, risks, summary string, threshold int) string {
	// The audit report now emits its own "## 🗣 Em resumo" and
	// "## 🎯 Certeza de Sucesso". On a verified round the header below already
	// carries the authoritative summary + certainty — strip the audit's echo so
	// the ticket doesn't show both twice.
	original = stripAuditEcho(original)
	// Extract the headers we keep (title + labels + other sections) and
	// replace only the "Plano de Correção" section.
	planSectionRe := regexp.MustCompile(`(?ims)(^##\s*🛠?\s*Plano\s+de\s+Corre[cç][ãa]o[^\n]*\n)(.+?)(\n##\s|\z)`)
	loc := planSectionRe.FindStringSubmatchIndex(original)
	if loc == nil {
		// The expected section wasn't found — return the refined text as a raw
		// plan, but with the verification block on top.
		return formatVerificationHeader(verdict, certainty, risks, summary, threshold) + "\n\n" + refined
	}
	// Replace [header][old plan][next section] with [header][refined][next section]
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
	// Plain-language summary — it sits ABOVE the risks so whoever opened the
	// ticket understands, without jargon, what the plan does to the site, what
	// will be fixed and what the problem was, before reaching the technical detail.
	if s := strings.TrimSpace(summary); s != "" {
		b.WriteString("\n**📣 What this plan does (in short):**\n")
		b.WriteString(s)
		b.WriteString("\n")
	}
	if strings.TrimSpace(risks) != "" && !strings.EqualFold(strings.TrimSpace(risks), "Nenhum identificado") {
		b.WriteString("\n**Risks identified:**\n")
		b.WriteString(risks)
		b.WriteString("\n")
	}
	return b.String()
}

// stripBody removes the "## 📝 Título sugerido" block (already used as
// the new summary) from the report before inlining it into the
// description — avoid duplicating the title in both fields.
func stripBody(report string) string {
	// remove from "## 📝 Título sugerido" through (but not including)
	// the next "## " heading
	startRe := regexp.MustCompile(`(?m)^##\s*📝?\s*[Tt]ítulo[^\n]*\n`)
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

// sectionHeadRe matches the start of any "## " markdown section heading.
var sectionHeadRe = regexp.MustCompile(`(?m)^##\s`)

// auditSummaryHeadRe / auditCertaintyHeadRe match the header LINE (through its
// trailing newline) of the two informational blocks the audit contract now
// emits. The optional emoji mirrors summaryRe/certaintyRe so a model that drops
// the emoji is still matched.
var (
	auditSummaryHeadRe   = regexp.MustCompile(`(?m)^##\s*🗣️?\s*Em\s+resumo[^\n]*\n`)
	auditCertaintyHeadRe = regexp.MustCompile(`(?m)^##\s*🎯?\s*Certeza\s+de\s+Sucesso[^\n]*\n`)
)

// stripSection removes one "## …" section from s — the header line matched by
// headerRe plus its body, up to (but not including) the next "## " heading or
// EOF. Returns s unchanged when the header is absent. Uses index math like
// stripBody because RE2 has no lookahead (a single lazy regex stopping before
// the next header would have to consume it).
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

// stripAuditEcho removes the audit report's own "## 🗣 Em resumo" and
// "## 🎯 Certeza de Sucesso" blocks. On a VERIFIED run, formatVerificationHeader
// already surfaces the authoritative (adversarially reviewed) summary + certainty
// at the top, so keeping the auditor's self-assessment too would duplicate both
// in the ticket. This runs only inside wrapRefinedPlan, which is reached only when
// a verify round produced a refined plan — so a verify-FAILED run keeps the raw
// audit (with its own summary/certainty) untouched, exactly the fallback we want.
func stripAuditEcho(s string) string {
	s = stripSection(s, auditSummaryHeadRe)
	s = stripSection(s, auditCertaintyHeadRe)
	return s
}

// AI block markers — must match exactly so re-runs replace the previous
// block instead of appending forever.
const (
	aiBlockStart = "\n\n--- 🤖 AI ANALYSIS ---\n"
	aiBlockEnd   = "\n--- /🤖 AI ANALYSIS ---"
)

// mergeAIBlock returns the new full description: original (or original
// stripped of any previous AI block) + a fresh AI block at the end.
func mergeAIBlock(existing, aiContent string) string {
	base := stripAIBlock(existing)
	stamp := time.Now().UTC().Format("2006-01-02 15:04 UTC")
	body := fmt.Sprintf("%s_(generated on %s — pressing the button again regenerates this block; the content above is preserved)_\n\n%s%s",
		aiBlockStart, stamp, strings.TrimSpace(aiContent), aiBlockEnd)
	return strings.TrimRight(base, "\n") + body
}

// stripAIBlock removes everything between aiBlockStart and aiBlockEnd
// (inclusive) so re-runs don't accumulate stale analyses.
func stripAIBlock(s string) string {
	i := strings.Index(s, aiBlockStart)
	if i < 0 {
		return s
	}
	j := strings.Index(s, aiBlockEnd)
	if j < 0 || j < i {
		// malformed (start without end) — drop from start to EOF
		return strings.TrimRight(s[:i], "\n")
	}
	return strings.TrimRight(s[:i]+s[j+len(aiBlockEnd):], "\n")
}

// extractAIBlock returns the INNER content of a prior AI block (everything
// between aiBlockStart and aiBlockEnd), or "" if there is no well-formed
// block. The leading "_(gerado em … )_" stamp line is dropped so the model
// isn't anchored on a timestamp when refining. This is the counterpart of
// stripAIBlock: stripAIBlock keeps the ticket minus the block, extractAIBlock
// keeps the block minus the ticket.
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
	// Drop the generated-at stamp line ("_(gerado em … )_") if present so the
	// refine prompt carries the plan, not the bookkeeping note.
	inner = stampLineRe.ReplaceAllString(inner, "")
	return strings.TrimSpace(inner)
}

// stampLineRe matches the italic "_(gerado em … )_" line mergeAIBlock prepends.
var stampLineRe = regexp.MustCompile(`(?m)^_\(generated on [^\n]*\)_\n?`)

// priorCertaintyRe captures the "**Certeza:** NN%" written by
// formatVerificationHeader into the description block on a verified run.
var priorCertaintyRe = regexp.MustCompile(`(?i)\*\*Certeza:\*\*\s*(\d{1,3})\s*%`)

// parsePriorCertainty reads the certainty a previous run recorded inside the
// AI block. Returns 0 when there's no verified block (first run, or a run that
// produced no verification header) — callers treat 0 as "no ratchet, use the
// floor threshold".
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

// ── helpers ──────────────────────────────────────────────────────────

func nameOf(n *jira.NamedRef) string {
	if n == nil {
		return ""
	}
	return n.Name
}

// adfDescLimit is the conservative ceiling (in bytes) for descriptions and
// comments sent to Jira. The ADF format blows up around ~32KB; 30000 leaves
// room for the ADF serialisation overhead done in the jira package.
const adfDescLimit = 30000

// clampForADF truncates s to fit in limit bytes, cutting on a rune boundary
// and appending a note. Returns (text, true) if it truncated.
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

// writeIssueMeta emits the shared ticket metadata block (Ticket/Tipo/
// Prioridade/Status/Labels/Repo + Título) used by both the audit and refine
// prompts. cleanDesc is the ORIGINAL ticket description with any prior AI
// block already stripped (see Run) — keeping the model's own previous output
// out of the "user ask" so it can't be mistaken for the requirement.
func writeIssueMeta(b *strings.Builder, d *jira.IssueDetail, projectKey, repoPath, cleanDesc string) {
	b.WriteString("**Ticket:** ")
	b.WriteString(d.Key)
	if projectKey != "" {
		b.WriteString(" (projeto: ")
		b.WriteString(projectKey)
		b.WriteString(")")
	}
	b.WriteString("\n")
	if d.IssueType != nil {
		b.WriteString("**Tipo:** ")
		b.WriteString(d.IssueType.Name)
		b.WriteString("\n")
	}
	if d.Priority != nil {
		b.WriteString("**Prioridade:** ")
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

// buildPrompt assembles the auditor prompt (FIRST analysis): the preamble
// (editable brain + locked contract, coming from the registry) followed by the
// issue data block, which stays assembled in Go. cleanDesc is the original
// description with any prior AI block removed.
func buildPrompt(d *jira.IssueDetail, cleanDesc, projectKey, repoPath, preamble string) string {
	var b strings.Builder
	b.WriteString(preamble)
	b.WriteString("\n\n---\n\n")
	writeIssueMeta(&b, d, projectKey, repoPath, cleanDesc)
	return b.String()
}

// buildRefinePrompt assembles the RE-RUN prompt: on top of the ticket metadata
// it injects the previous plan (extracted from the description's AI block) with
// its measured certainty, and closes with the explicit instruction to beat it.
// This is what turns pressing the button again into "harden the existing plan"
// rather than a fresh audit from scratch. It uses the same output contract as
// the audit, so downstream parsing (title/labels/plan) is identical.
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

// buildCommentBody wraps the AI report with a header so it's clearly an
// automated analysis when read on the Jira UI. It includes the result of the
// iterative verification (certainty % + rounds) so the reviewer knows at a
// glance whether the plan was approved or needs a human look.
func buildCommentBody(report, issueKey string, certainty, rounds, priorCertainty, threshold int) string {
	stamp := time.Now().UTC().Format("2006-01-02 15:04 UTC")
	verdict := "⚠️ human review recommended"
	if certainty >= threshold {
		verdict = "✅ approved automatically"
	}
	// Progression across re-runs: shows that certainty is improving (or not)
	// each time the button runs. It only appears when there was a prior plan.
	progress := ""
	if priorCertainty > 0 {
		progress = fmt.Sprintf(" · progress %d%% → %d%% (%+d)", priorCertainty, certainty, certainty-priorCertainty)
	}
	header := fmt.Sprintf(
		"🤖 *Automated analysis (vps-manager AI · %s)*\n\n"+
			"Generated from the title + description of ticket %s.\n\n"+
			"**Iterative verification:** %s · confidence %d%% (target %d%%) · %d round(s)%s\n",
		stamp, issueKey, verdict, certainty, threshold, rounds, progress,
	)
	return header + "\n---\n\n" + report
}

// titleLineRe matches "## 📝 Título sugerido" (or just "## Título") followed
// by the first non-empty line below.
var titleLineRe = regexp.MustCompile(`(?m)^##\s*📝?\s*[Tt]ítulo[^\n]*\n+([^\n]+)`)

// parseSuggestedTitle extracts the AI-proposed summary. Strips quotes,
// trims trivial decorations, and clamps to Jira's 255-char limit.
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

// labelLine matches the "## 🏷 Labels" header followed by a line of
// comma-separated tokens.
var labelLineRe = regexp.MustCompile(`(?m)^##\s*🏷?\s*Labels[^\n]*\n+([^\n]+)`)

// parseSuggestedLabels extracts the labels line and normalises each token
// to kebab-case Jira-compatible. Falls back to an empty list if absent.
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
		// Jira labels: alphanumeric + - + _ only, length <= 255
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

// mergeLabels returns existing ∪ new (dedup, stable order: existing first).
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
