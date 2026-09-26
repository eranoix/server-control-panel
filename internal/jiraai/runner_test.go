package jiraai

import (
	"strings"
	"testing"

	"server-control-panel/internal/aiprompts"
	"server-control-panel/internal/jira"
)

// ── parser ↔ contract conformance ────────────────────────────────────
//
// These guard the convergence loop against silent breakage: if a parser
// and the locked output contract (aiprompts/defaults.go) ever drift, the
// loop would stop converging. The canonical examples are model-output
// samples that MUST conform to the contract.

func TestContractParsesClean(t *testing.T) {
	// Verify-side parsers against the canonical verify response.
	if v := parseVerdict(aiprompts.CanonicalVerifyResponse); v != "APROVADO" {
		t.Errorf("parseVerdict = %q, want APROVADO — verify contract drifted from verdictRe", v)
	}
	if c := parseCertainty(aiprompts.CanonicalVerifyResponse); c != 92 {
		t.Errorf("parseCertainty = %d, want 92 — Certeza header/regex drifted", c)
	}
	if p := parseRefinedPlan(aiprompts.CanonicalVerifyResponse); p == "" {
		t.Error("parseRefinedPlan empty — '## 📋 Plano Final' header drifted from refinedPlanRe")
	}
	if r := parseRisks(aiprompts.CanonicalVerifyResponse); r == "" {
		t.Error("parseRisks empty — '## ⚠️ Riscos' header drifted from risksRe")
	}
	if s := parseSummary(aiprompts.CanonicalVerifyResponse); s == "" {
		t.Error("parseSummary empty — '## 🗣 Em resumo' header drifted from summaryRe")
	}
	// Audit-side parsers against the canonical audit response.
	if title := parseSuggestedTitle(aiprompts.CanonicalAuditResponse); title == "" {
		t.Error("parseSuggestedTitle empty — '## 📝 Título' header drifted from titleLineRe")
	}
	if labels := parseSuggestedLabels(aiprompts.CanonicalAuditResponse); len(labels) == 0 {
		t.Error("parseSuggestedLabels empty — '## 🏷 Labels' header drifted from labelLineRe")
	}
	// wrapRefinedPlan must locate the '## 🛠 Plano de Correção' section in the
	// audit response and splice — not fall back to the raw-dump path (which
	// would drop the title block).
	wrapped := wrapRefinedPlan(aiprompts.CanonicalAuditResponse, "PLANO-REFINADO-XYZ", "APROVADO", 90, "alguns riscos", "resumo em linguagem simples", verifyAcceptThreshold)
	if !strings.Contains(wrapped, "PLANO-REFINADO-XYZ") {
		t.Error("wrapRefinedPlan dropped the refined plan")
	}
	if !strings.Contains(wrapped, "## 📝 Título sugerido") {
		t.Error("wrapRefinedPlan fell back to raw dump — planSectionRe drifted from auditContract")
	}
	// The plain-language summary must appear ABOVE the risks block.
	if !strings.Contains(wrapped, "resumo em linguagem simples") {
		t.Error("wrapRefinedPlan dropped the plain-language summary")
	}
	if i, j := strings.Index(wrapped, "resumo em linguagem simples"), strings.Index(wrapped, "Risks identified"); i < 0 || j < 0 || i > j {
		t.Error("plain-language summary must render ABOVE 'Risks identified'")
	}
}

// TestWrapRefinedPlanStripsAuditEcho guards the dedup added when the audit
// contract started emitting its own "## 🗣 Em resumo" + "## 🎯 Certeza de
// Sucesso". On a verified run, formatVerificationHeader surfaces the
// authoritative summary + certainty at the top, so wrapRefinedPlan must strip
// the auditor's echo of both — otherwise the ticket shows two summaries and two
// certainty numbers. Other audit sections (Título, Labels) must survive.
func TestWrapRefinedPlanStripsAuditEcho(t *testing.T) {
	// Sanity: the canonical audit sample carries the two echo headers.
	if !strings.Contains(aiprompts.CanonicalAuditResponse, "## 🗣 Em resumo") ||
		!strings.Contains(aiprompts.CanonicalAuditResponse, "## 🎯 Certeza de Sucesso") {
		t.Fatal("precondition: CanonicalAuditResponse should contain both echo headers")
	}
	wrapped := wrapRefinedPlan(aiprompts.CanonicalAuditResponse, "PLANO-REFINADO-XYZ",
		"APROVADO", 90, "alguns riscos", "RESUMO-DO-VERIFY", verifyAcceptThreshold)

	if strings.Contains(wrapped, "## 🗣 Em resumo") {
		t.Error("audit '## 🗣 Em resumo' echo not stripped — duplicates the verify summary")
	}
	if strings.Contains(wrapped, "## 🎯 Certeza de Sucesso") {
		t.Error("audit '## 🎯 Certeza de Sucesso' echo not stripped — duplicates the verify certainty")
	}
	// The authoritative verify header + its plain-language summary must remain.
	if !strings.Contains(wrapped, "## 🔬 Verification") {
		t.Error("verify header missing from wrapped plan")
	}
	if !strings.Contains(wrapped, "RESUMO-DO-VERIFY") {
		t.Error("verify summary dropped from wrapped plan")
	}
	// Non-echo audit sections must survive the strip.
	if !strings.Contains(wrapped, "## 📝 Título sugerido") || !strings.Contains(wrapped, "## 🏷 Labels") {
		t.Error("stripAuditEcho removed sections other than the two echo blocks")
	}
}

// TestStripAuditEchoNoEcho: a report WITHOUT the echo blocks is returned with
// those sections simply absent and everything else intact (idempotent / safe on
// the verify-failed fallback shape).
func TestStripAuditEchoNoEcho(t *testing.T) {
	in := "## 📝 Título sugerido\nT\n\n## 🛠 Plano de Correção\n1. x\n\n## 🏷 Labels\na, b\n"
	if got := stripAuditEcho(in); got != in {
		t.Errorf("stripAuditEcho mutated a report with no echo blocks:\n%q", got)
	}
}

// TestCertaintyInlineRegression is the core structural-regression guard:
// a response that keeps the marker but puts the number INLINE (same line as
// the header) instead of on the line below must parse as 0 certainty. This
// is exactly the "marker present but structure wrong" case that would make
// the loop run to the budget on every analysis. The defense is that the
// contract (which mandates the line-below layout) is NOT runtime-editable.
func TestCertaintyInlineRegression(t *testing.T) {
	inline := "## ✅ Veredicto\nAPROVADO\n\n## 🎯 Certeza de Sucesso 92\n\n## ⚠️ Riscos\n- nada\n"
	if v := parseVerdict(inline); v != "APROVADO" {
		t.Errorf("sanity: parseVerdict = %q, want APROVADO", v)
	}
	if c := parseCertainty(inline); c != 0 {
		t.Errorf("parseCertainty(inline) = %d, want 0 — inline number must NOT parse "+
			"(structural contract: number goes on the line below the header)", c)
	}
}

// TestVerifyPreambleAssembly proves the assembled reviewer prompt carries
// the locked contract headers, has the threshold substituted, and exposes
// no unresolved placeholders.
func TestVerifyPreambleAssembly(t *testing.T) {
	reg := aiprompts.New(t.TempDir())
	pre := reg.VerifyPreamble(85)
	for _, h := range []string{"## ✅ Veredicto", "## 🎯 Certeza de Sucesso", "## 🗣 Em resumo", "## 📋 Plano Final"} {
		if !strings.Contains(pre, h) {
			t.Errorf("assembled verify preamble missing locked header %q", h)
		}
	}
	if !strings.Contains(pre, "85%") {
		t.Error("threshold 85 not substituted into the contract")
	}
	if strings.Contains(pre, aiprompts.PlaceholderContract) || strings.Contains(pre, aiprompts.PlaceholderThreshold) {
		t.Error("unresolved placeholder left in assembled preamble")
	}
}

func TestParseSuggestedLabels(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{
			"basic",
			"## 🔍 Diagnóstico\n...\n## 🏷 Labels\nbackend, security, refactor",
			[]string{"backend", "security", "refactor"},
		},
		{
			"emoji-less",
			"## Labels\nbug-fix,perf,tests",
			[]string{"bug-fix", "perf", "tests"},
		},
		{
			"normalises spaces and case",
			"## 🏷 Labels\nBack End, Bug Fix, ux/ui",
			// "ux/ui" has a slash → rejected; the others normalise
			[]string{"back-end", "bug-fix"},
		},
		{
			"dedup",
			"## 🏷 Labels\nbug, bug, perf, perf, perf",
			[]string{"bug", "perf"},
		},
		{
			"missing block",
			"## Diagnóstico\nfoo",
			nil,
		},
		{
			"strips markdown decoration",
			"## 🏷 Labels\n`backend`, *security*, _refactor_",
			[]string{"backend", "security", "refactor"},
		},
	}
	for _, c := range cases {
		got := parseSuggestedLabels(c.in)
		if !equalStrings(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestMergeLabels(t *testing.T) {
	got := mergeLabels([]string{"a", "b"}, []string{"b", "c", "ai-analyzed"})
	want := []string{"a", "b", "c", "ai-analyzed"}
	if !equalStrings(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestLabelsEqual(t *testing.T) {
	if !labelsEqual([]string{"a", "b"}, []string{"a", "b"}) {
		t.Error("equal should be true")
	}
	if labelsEqual([]string{"a", "b"}, []string{"b", "a"}) {
		t.Error("order matters — diff order should be false")
	}
	if labelsEqual([]string{"a"}, []string{"a", "b"}) {
		t.Error("different lengths should be false")
	}
}

func TestParseSuggestedTitle(t *testing.T) {
	cases := []struct{ in, want string }{
		{"## 📝 Título sugerido\nFix: race em queue\n## 🔍 Diag\n...", "Fix: race em queue"},
		{"## Título\n  Novo título de bug  ", "Novo título de bug"},
		{`## 📝 Título sugerido
"Resolver duplicidade de perfil ao re-entrar"
## next`, "Resolver duplicidade de perfil ao re-entrar"},
		{"## 📝 Título\n`Backquoted`\n##", "Backquoted"},
		{"sem header", ""},
	}
	for _, c := range cases {
		got := parseSuggestedTitle(c.in)
		if got != c.want {
			t.Errorf("input=%q\n got=%q\nwant=%q", c.in, got, c.want)
		}
	}
}

func TestMergeAIBlock(t *testing.T) {
	// First run: appends block to existing description.
	out := mergeAIBlock("descrição original", "conteúdo análise")
	if !contains(out, "descrição original") || !contains(out, "🤖 AI ANALYSIS") || !contains(out, "conteúdo análise") {
		t.Errorf("first run lost something: %q", out)
	}
	// Second run: replaces previous block, original survives.
	out2 := mergeAIBlock(out, "nova análise diferente")
	if !contains(out2, "descrição original") {
		t.Error("re-run lost original description")
	}
	if contains(out2, "conteúdo análise") {
		t.Errorf("re-run kept stale AI block: %q", out2)
	}
	if !contains(out2, "nova análise diferente") {
		t.Errorf("re-run lost new AI content: %q", out2)
	}
}

func TestStripAIBlockNoOp(t *testing.T) {
	if got := stripAIBlock("plain description"); got != "plain description" {
		t.Errorf("strip-no-block changed input: %q", got)
	}
}

func TestStripBody(t *testing.T) {
	report := `## 📝 Título sugerido
Meu título

## 🔍 Diagnóstico
diagnóstico aqui

## 🏷 Labels
foo, bar`
	got := stripBody(report)
	if contains(got, "Meu título") {
		t.Errorf("stripBody should have removed the title block: %q", got)
	}
	if !contains(got, "Diagnóstico") {
		t.Errorf("stripBody dropped diagnóstico: %q", got)
	}
}

// ── re-run: iteration awareness + certainty ratchet ───────────────────

// extractAIBlock must return the inner plan (minus the generated-at stamp)
// and be the exact complement of stripAIBlock: original survives in one,
// the plan in the other, with no overlap.
func TestExtractAIBlock(t *testing.T) {
	desc := mergeAIBlock("ticket original do usuário", "## 🔬 Verificação\n**Veredicto:** APROVADO · **Certeza:** 82%\n\n## 🛠 Plano de Correção\n1. passo (a.go:1)")
	block := extractAIBlock(desc)
	if block == "" {
		t.Fatal("extractAIBlock returned empty for a well-formed block")
	}
	if contains(block, "ticket original do usuário") {
		t.Errorf("extractAIBlock leaked the original ticket text: %q", block)
	}
	if contains(block, "generated on") {
		t.Errorf("extractAIBlock kept the generated-at stamp line: %q", block)
	}
	if !contains(block, "Plano de Correção") {
		t.Errorf("extractAIBlock dropped the plan body: %q", block)
	}
	// No prior block → empty.
	if got := extractAIBlock("descrição sem bloco AI"); got != "" {
		t.Errorf("extractAIBlock on plain text = %q, want empty", got)
	}
}

func TestParsePriorCertainty(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"**Veredicto:** APROVADO · **Certeza:** 82%", 82},
		{"**certeza:** 7 %", 7},
		{"**Certeza:** 140%", 100}, // clamped
		{"nada aqui", 0},
		{"", 0},
	}
	for _, c := range cases {
		if got := parsePriorCertainty(c.in); got != c.want {
			t.Errorf("parsePriorCertainty(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

// ratchetThreshold: first run (prior 0) = floor; a prior at/above the floor
// raises the bar a step; never exceeds the cap.
func TestRatchetThreshold(t *testing.T) {
	cases := []struct {
		prior, want int
	}{
		{0, verifyAcceptThreshold},                                   // first run
		{40, verifyAcceptThreshold},                                  // below floor → floor
		{verifyAcceptThreshold, verifyAcceptThreshold + ratchetStep}, // at floor → step up
		{90, 94},
		{96, ratchetCap},  // would be 100 → capped
		{100, ratchetCap}, // already maxed → capped
	}
	for _, c := range cases {
		if got := ratchetThreshold(c.prior); got != c.want {
			t.Errorf("ratchetThreshold(%d) = %d, want %d", c.prior, got, c.want)
		}
	}
}

// buildRefinePrompt must carry the prior plan, its certainty, and the
// "beat it" directive — and keep the original ticket separate from the plan.
func TestBuildRefinePrompt(t *testing.T) {
	d := &jira.IssueDetail{Issue: jira.Issue{Key: "TASK-99", Summary: "título do ticket"}}
	p := buildRefinePrompt(d, "descrição original limpa", "## 🛠 Plano de Correção\n1. passo antigo", 78,
		"VPSM", "/repo", "PREÂMBULO-REFINE")
	for _, want := range []string{"PREÂMBULO-REFINE", "TASK-99", "descrição original limpa", "passo antigo", "78%", "PREVIOUS PLAN", "beat it"} {
		if !contains(p, want) {
			t.Errorf("buildRefinePrompt missing %q", want)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func equalStrings(a, b []string) bool {
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
