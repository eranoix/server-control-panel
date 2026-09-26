// Package aiprompts is the runtime registry for the editable AI prompts
// used by the Jira "Iniciar AI" feature.
//
// Each prompt has a compiled-in default (defaults.go) and an optional
// admin override persisted to <DataDir>/ai_prompts.json. The registry is
// read by internal/jiraai at job time and written by the gated
// GET/PUT /api/ai/prompts endpoint.
//
// Only the instructional "brain" of each prompt is editable; the output
// CONTRACT (the exact markdown headers the Go parsers read) is locked and
// spliced in at the {{OUTPUT_CONTRACT}} placeholder. See defaults.go for
// the full rationale.
package aiprompts

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Placeholder tokens recognised inside editable templates.
const (
	PlaceholderContract  = "{{OUTPUT_CONTRACT}}"
	PlaceholderThreshold = "{{THRESHOLD}}"
)

// ID identifies an editable prompt.
type ID string

const (
	Audit  ID = "audit"
	Refine ID = "refine"
	Verify ID = "verify"
	Work   ID = "work"
)

// spec is the immutable description of a prompt: its default text, the
// locked contract to splice (empty = none), and whether the contract
// placeholder is mandatory in any override.
type spec struct {
	id              ID
	title           string
	description     string
	def             string
	contract        string // "" → no contract (e.g. Work)
	requireContract bool
}

// order is the stable display/iteration order.
var order = []ID{Audit, Refine, Verify, Work}

var specs = map[ID]spec{
	Audit: {
		id:              Audit,
		title:           "Audit (Analyze with Claude)",
		description:     "Preamble for the auditor that reads the repository and produces the fix plan on the FIRST analysis (ticket still has no AI plan). The output contract (Title/In short/Diagnosis/Audit/Plan/Improvements/Confidence of Success/Labels headers) is managed by the system and injected at {{OUTPUT_CONTRACT}}.",
		def:             auditPreambleDefault,
		contract:        auditContract,
		requireContract: true,
	},
	Refine: {
		id:              Refine,
		title:           "Refinement (re-run to improve an existing plan)",
		description:     "Preamble used when the ticket ALREADY has an AI plan (pressing the button again). Instead of starting from scratch, it tells Claude to harden the previous plan, resolve the risks that held confidence down and beat the previous round's confidence. Same output contract as the Audit (includes In short + Confidence of Success).",
		def:             refinePreambleDefault,
		contract:        auditContract,
		requireContract: true,
	},
	Verify: {
		id:              Verify,
		title:           "Verification (convergent loop)",
		description:     "Preamble for the adversarial reviewer that verifies and refines the plan until it reaches the confidence threshold. The contract (Verdict/Confidence/Risks/Final Plan) is managed by the system; {{THRESHOLD}} is injected at runtime.",
		def:             verifyPreambleDefault,
		contract:        verifyContract,
		requireContract: true,
	},
	Work: {
		id:              Work,
		title:           "Work on it now (terminal session)",
		description:     "Final instruction pasted into the Claude session when 'Work on it now' is opened. No output contract — an interactive, fully editable session.",
		def:             workTrailerDefault,
		contract:        "",
		requireContract: false,
	},
}

// Registry holds the current overrides, persisted atomically.
type Registry struct {
	mu        sync.RWMutex
	path      string
	overrides map[ID]string
}

// New opens (or initialises) the registry at <dataDir>/ai_prompts.json.
// A missing/corrupt file is tolerated — the registry falls back to
// compiled-in defaults and the first successful Set rewrites the file.
func New(dataDir string) *Registry {
	r := &Registry{
		path:      filepath.Join(dataDir, "ai_prompts.json"),
		overrides: map[ID]string{},
	}
	r.load()
	return r
}

func (r *Registry) load() {
	b, err := os.ReadFile(r.path)
	if err != nil {
		return // missing → defaults
	}
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		return // corrupt → defaults
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for k, v := range m {
		id := ID(k)
		if _, ok := specs[id]; ok && strings.TrimSpace(v) != "" {
			r.overrides[id] = v
		}
	}
}

// template returns the editable template (override if present, else
// default) for id.
func (r *Registry) template(id ID) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if v, ok := r.overrides[id]; ok {
		return v
	}
	if s, ok := specs[id]; ok {
		return s.def
	}
	return ""
}

// assemble splices the locked contract into an editable template and
// substitutes the threshold. If the template lacks the placeholder (which
// Validate rejects for contract-required prompts), the contract is appended
// so assembly never silently drops the machine-readable section.
func assemble(template string, s spec, threshold int) string {
	if s.contract == "" {
		return template
	}
	c := s.contract
	if threshold > 0 {
		c = strings.ReplaceAll(c, PlaceholderThreshold, fmt.Sprintf("%d", threshold))
	}
	if strings.Contains(template, PlaceholderContract) {
		return strings.ReplaceAll(template, PlaceholderContract, c)
	}
	return strings.TrimRight(template, "\n") + "\n\n" + c
}

// AuditPreamble returns the assembled auditor preamble (editable brain +
// locked contract). The caller appends the ticket data block.
func (r *Registry) AuditPreamble() string {
	return assemble(r.template(Audit), specs[Audit], 0)
}

// RefinePreamble returns the assembled "improve the existing plan" preamble
// used on re-runs. Same locked contract as Audit (identical output sections),
// so the runner parses both paths with the same regexes.
func (r *Registry) RefinePreamble() string {
	return assemble(r.template(Refine), specs[Refine], 0)
}

// VerifyPreamble returns the assembled reviewer preamble with the accept
// threshold injected into the locked contract.
func (r *Registry) VerifyPreamble(threshold int) string {
	return assemble(r.template(Verify), specs[Verify], threshold)
}

// WorkTrailer returns the editable "Trabalhar agora" instruction block.
func (r *Registry) WorkTrailer() string {
	return r.template(Work)
}

// View is the JSON shape returned by GET /api/ai/prompts for one prompt.
type View struct {
	ID              ID     `json:"id"`
	Title           string `json:"title"`
	Description     string `json:"description"`
	Default         string `json:"default"`
	Value           string `json:"value"`
	Overridden      bool   `json:"overridden"`
	Contract        string `json:"contract,omitempty"` // locked block, shown read-only
	RequireContract bool   `json:"require_contract"`
}

// List returns every prompt's current state for the UI, in stable order.
func (r *Registry) List() []View {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]View, 0, len(order))
	for _, id := range order {
		s := specs[id]
		v, overridden := r.overrides[id]
		if !overridden {
			v = s.def
		}
		out = append(out, View{
			ID:              id,
			Title:           s.title,
			Description:     s.description,
			Default:         s.def,
			Value:           v,
			Overridden:      overridden,
			Contract:        s.contract,
			RequireContract: s.requireContract,
		})
	}
	return out
}

// Validate checks a candidate override for id. Returns a list of
// human-readable failures (empty = OK). This is the conformance guard the
// PUT endpoint runs before persisting — it guarantees the editable template
// still carries the contract placeholder, so the locked headers the Go
// parsers depend on can never be edited away.
func Validate(id ID, candidate string) []string {
	s, ok := specs[id]
	if !ok {
		return []string{fmt.Sprintf("unknown prompt: %q", id)}
	}
	var fails []string
	if strings.TrimSpace(candidate) == "" {
		fails = append(fails, "the prompt cannot be empty")
		return fails
	}
	if s.requireContract && !strings.Contains(candidate, PlaceholderContract) {
		fails = append(fails, fmt.Sprintf(
			"the %s marker is missing — it is required: it is where the system injects the output contract (the headers the parsers read). Without it the loop never converges.",
			PlaceholderContract))
	}
	// A stray {{THRESHOLD}} in the editable part would never be substituted
	// (substitution happens only inside the locked contract). Flag it so the
	// admin doesn't think they can inject the threshold from the brain.
	if strings.Contains(candidate, PlaceholderThreshold) {
		fails = append(fails, fmt.Sprintf(
			"%s does not work here — the threshold is injected automatically into the managed contract. Remove it from the editable text.",
			PlaceholderThreshold))
	}
	return fails
}

// Set validates and atomically persists an override for id. Passing the
// exact default text (or empty) clears the override (reset to default).
func (r *Registry) Set(id ID, candidate string) []string {
	s, ok := specs[id]
	if !ok {
		return []string{fmt.Sprintf("unknown prompt: %q", id)}
	}
	reset := strings.TrimSpace(candidate) == "" || strings.TrimSpace(candidate) == strings.TrimSpace(s.def)
	if !reset {
		if fails := Validate(id, candidate); len(fails) > 0 {
			return fails
		}
	}
	r.mu.Lock()
	if reset {
		delete(r.overrides, id)
	} else {
		r.overrides[id] = candidate
	}
	snapshot := make(map[string]string, len(r.overrides))
	for k, v := range r.overrides {
		snapshot[string(k)] = v
	}
	r.mu.Unlock()

	if err := r.persist(snapshot); err != nil {
		return []string{"failed to save: " + err.Error()}
	}
	return nil
}

// persist writes the overrides map atomically (tmp + rename), mirroring
// handleUserPrefs (api.go).
func (r *Registry) persist(snapshot map[string]string) error {
	out, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o700); err != nil {
		return err
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, r.path)
}
