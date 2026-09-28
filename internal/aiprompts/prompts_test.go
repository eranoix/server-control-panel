package aiprompts

import (
	"strings"
	"testing"
)

func TestValidateRequiresContract(t *testing.T) {
	if fails := Validate(Verify, "smart prompt but without the marker"); len(fails) == 0 {
		t.Error("Validate(Verify) accepted a template without {{OUTPUT_CONTRACT}}")
	}
	if fails := Validate(Verify, "smart prompt with "+PlaceholderContract+" in the middle"); len(fails) != 0 {
		t.Errorf("Validate(Verify) rejected a valid template: %v", fails)
	}
	if fails := Validate(Audit, "auditor without a contract"); len(fails) == 0 {
		t.Error("Validate(Audit) accepted a template without {{OUTPUT_CONTRACT}}")
	}
}

func TestValidateWorkNoContract(t *testing.T) {
	if fails := Validate(Work, "any free-form instruction"); len(fails) != 0 {
		t.Errorf("Validate(Work) should not require a contract: %v", fails)
	}
	if fails := Validate(Work, ""); len(fails) == 0 {
		t.Error("Validate(Work) accepted an empty prompt")
	}
}

func TestValidateRejectsThresholdInBrain(t *testing.T) {
	cand := "use at least " + PlaceholderThreshold + "% and " + PlaceholderContract
	fails := Validate(Verify, cand)
	if len(fails) == 0 {
		t.Error("Validate should flag a {{THRESHOLD}} placed in the editable part")
	}
}

func TestSetResetAndPersist(t *testing.T) {
	dir := t.TempDir()
	reg := New(dir)

	custom := "Custom, smarter preamble.\n\n" + PlaceholderContract
	if fails := reg.Set(Verify, custom); fails != nil {
		t.Fatalf("Set(Verify) valid override failed: %v", fails)
	}
	reg2 := New(dir)
	var v View
	for _, p := range reg2.List() {
		if p.ID == Verify {
			v = p
		}
	}
	if !v.Overridden || !strings.Contains(v.Value, "Custom") {
		t.Errorf("override not persisted/loaded: overridden=%v value=%.40q", v.Overridden, v.Value)
	}

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

func TestSetRejectsInvalid(t *testing.T) {
	dir := t.TempDir()
	reg := New(dir)
	if fails := reg.Set(Verify, "no contract at all"); len(fails) == 0 {
		t.Fatal("Set accepted an invalid override")
	}
	for _, p := range New(dir).List() {
		if p.ID == Verify && p.Overridden {
			t.Error("rejected override leaked into persisted state")
		}
	}
}

func TestDefaultsAreValid(t *testing.T) {
	for _, id := range []ID{Audit, Refine, Verify} {
		if fails := Validate(id, specs[id].def); len(fails) != 0 {
			t.Errorf("default %s fails its own guard: %v", id, fails)
		}
	}
}

func TestRefinePreambleAssembly(t *testing.T) {
	reg := New(t.TempDir())
	pre := reg.RefinePreamble()
	for _, h := range []string{"## 📝 Suggested title", "## 🛠 Fix Plan", "## 🏷 Labels"} {
		if !strings.Contains(pre, h) {
			t.Errorf("assembled refine preamble missing locked audit header %q", h)
		}
	}
	if strings.Contains(pre, PlaceholderContract) {
		t.Error("unresolved {{OUTPUT_CONTRACT}} left in assembled refine preamble")
	}
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
