package aiprompts

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	PlaceholderContract  = "{{OUTPUT_CONTRACT}}"
	PlaceholderThreshold = "{{THRESHOLD}}"
)

type ID string

const (
	Audit  ID = "audit"
	Refine ID = "refine"
	Verify ID = "verify"
	Work   ID = "work"
)

type spec struct {
	id              ID
	title           string
	description     string
	def             string
	contract        string
	requireContract bool
}

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

type Registry struct {
	mu        sync.RWMutex
	path      string
	overrides map[ID]string
}

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
		return
	}
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		return
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

func (r *Registry) AuditPreamble() string {
	return assemble(r.template(Audit), specs[Audit], 0)
}

func (r *Registry) RefinePreamble() string {
	return assemble(r.template(Refine), specs[Refine], 0)
}

func (r *Registry) VerifyPreamble(threshold int) string {
	return assemble(r.template(Verify), specs[Verify], threshold)
}

func (r *Registry) WorkTrailer() string {
	return r.template(Work)
}

type View struct {
	ID              ID     `json:"id"`
	Title           string `json:"title"`
	Description     string `json:"description"`
	Default         string `json:"default"`
	Value           string `json:"value"`
	Overridden      bool   `json:"overridden"`
	Contract        string `json:"contract,omitempty"`
	RequireContract bool   `json:"require_contract"`
}

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
	if strings.Contains(candidate, PlaceholderThreshold) {
		fails = append(fails, fmt.Sprintf(
			"%s does not work here — the threshold is injected automatically into the managed contract. Remove it from the editable text.",
			PlaceholderThreshold))
	}
	return fails
}

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
