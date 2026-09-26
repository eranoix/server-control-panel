package aiprompts

import (
	"strings"
	"testing"
)

// TestValidateRequiresContract is the conformance guard the PUT endpoint
// relies on: an audit/verify override that drops {{OUTPUT_CONTRACT}} must be
// rejected (that placeholder is where the locked, parser-matched headers are
// spliced — removing it would silently break the convergence loop).
func TestValidateRequiresContract(t *testing.T) {
	if fails := Validate(Verify, "prompt esperto mas sem o marcador"); len(fails) == 0 {
		t.Error("Validate(Verify) accepted a template without {{OUTPUT_CONTRACT}}")
	}
	if fails := Validate(Verify, "prompt esperto com "+PlaceholderContract+" no meio"); len(fails) != 0 {
		t.Errorf("Validate(Verify) rejected a valid template: %v", fails)
	}
	if fails := Validate(Audit, "auditor sem contrato"); len(fails) == 0 {
		t.Error("Validate(Audit) accepted a template without {{OUTPUT_CONTRACT}}")
	}
}

// Work has no machine-parsed output → no contract required.
func TestValidateWorkNoContract(t *testing.T) {
	if fails := Validate(Work, "qualquer instrução livre"); len(fails) != 0 {
		t.Errorf("Validate(Work) should not require a contract: %v", fails)
	}
	if fails := Validate(Work, ""); len(fails) == 0 {
		t.Error("Validate(Work) accepted an empty prompt")
	}
}

// A stray {{THRESHOLD}} in the editable brain never gets substituted (only
// the locked contract is) — the guard must flag it.
func TestValidateRejectsThresholdInBrain(t *testing.T) {
	cand := "use no mínimo " + PlaceholderThreshold + "% e " + PlaceholderContract
	fails := Validate(Verify, cand)
	if len(fails) == 0 {
		t.Error("Validate should flag a {{THRESHOLD}} placed in the editable part")
	}
}

func TestSetResetAndPersist(t *testing.T) {
	dir := t.TempDir()
	reg := New(dir)

	custom := "Preâmbulo customizado e mais inteligente.\n\n" + PlaceholderContract
	if fails := reg.Set(Verify, custom); fails != nil {
		t.Fatalf("Set(Verify) valid override failed: %v", fails)
	}
	// Override is reflected + persisted across a reopen.
	reg2 := New(dir)
	var v View
	for _, p := range reg2.List() {
		if p.ID == Verify {
			v = p
		}
	}
	if !v.Overridden || !strings.Contains(v.Value, "customizado") {
		t.Errorf("override not persisted/loaded: overridden=%v value=%.40q", v.Overridden, v.Value)
	}

	// Saving the default text clears the override (reset).
	if fails := reg2.Set(Verify, verifyPreambleDefault); fails != nil {
		t.Fatalf("reset Set(Verify, default) failed: %v", fails)
	}
	reg3 := New(dir)
	for _, p := range reg3.List() {
		if p.ID == Verify && p.Overridden {
			t.Error("reset did not clear the override")
		}
	}
}

// An invalid override must be rejected by Set (not just Validate) and leave
// no persisted state behind.
func TestSetRejectsInvalid(t *testing.T) {
	dir := t.TempDir()
	reg := New(dir)
	if fails := reg.Set(Verify, "sem contrato nenhum"); len(fails) == 0 {
		t.Fatal("Set accepted an invalid override")
	}
	for _, p := range New(dir).List() {
		if p.ID == Verify && p.Overridden {
			t.Error("rejected override leaked into persisted state")
		}
	}
}

// The compiled-in defaults must themselves pass the guard — otherwise the
// "reset to default" path could produce a template the loop can't parse.
func TestDefaultsAreValid(t *testing.T) {
	for _, id := range []ID{Audit, Refine, Verify} {
		if fails := Validate(id, specs[id].def); len(fails) != 0 {
			t.Errorf("default %s fails its own guard: %v", id, fails)
		}
	}
}

// The Refine prompt (re-run brain) reuses the AUDIT output contract, so its
// assembled preamble must carry the audit headers the runner parses (Título/
// Plano de Correção/Labels) with no leftover placeholder. If this drifts, a
// re-run's output stops parsing and the title/labels/plan extraction breaks.
func TestRefinePreambleAssembly(t *testing.T) {
	reg := New(t.TempDir())
	pre := reg.RefinePreamble()
	for _, h := range []string{"## 📝 Título sugerido", "## 🛠 Plano de Correção", "## 🏷 Labels"} {
		if !strings.Contains(pre, h) {
			t.Errorf("assembled refine preamble missing locked audit header %q", h)
		}
	}
	if strings.Contains(pre, PlaceholderContract) {
		t.Error("unresolved {{OUTPUT_CONTRACT}} left in assembled refine preamble")
	}
	// Refine must appear in the UI-facing List (so admins can edit it).
	var found bool
	for _, v := range reg.List() {
		if v.ID == Refine {
			found = true
		}
	}
	if !found {
		t.Error("Refine prompt missing from List() — UI won't show it")
	}
}
