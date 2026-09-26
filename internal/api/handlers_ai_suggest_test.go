package api

import "testing"

// parseSuggestJSON must tolerate the model wrapping JSON in prose or code fences.
func TestParseSuggestJSON(t *testing.T) {
	cases := []struct {
		in         string
		wantName   string
		wantDesc   string
		wantFailed bool
	}{
		{`{"name":"CPU saturada","description":"Avisa quando a CPU passar de 90%."}`, "CPU saturada", "Avisa quando a CPU passar de 90%.", false},
		{"Claro! Aqui está:\n```json\n{\"name\":\"Custo alto\",\"description\":\"Gasto do Claude acima do limite.\"}\n```", "Custo alto", "Gasto do Claude acima do limite.", false},
		{"sem json aqui", "", "", true},
		{`{"name":"  Trim  ","description":" ok "}`, "Trim", "ok", false},
	}
	for _, c := range cases {
		name, desc := parseSuggestJSON(c.in)
		if c.wantFailed {
			if name != "" || desc != "" {
				t.Errorf("expected failure for %q, got %q/%q", c.in, name, desc)
			}
			continue
		}
		if name != c.wantName || desc != c.wantDesc {
			t.Errorf("parseSuggestJSON(%q) = %q/%q, want %q/%q", c.in, name, desc, c.wantName, c.wantDesc)
		}
	}
}

// The prompt must include the metric context and demand pure JSON.
func TestBuildAlertSuggestPrompt(t *testing.T) {
	p := buildAlertSuggestPrompt(suggestAlertReq{Kind: "metric", Label: "CPU (usage)", Metric: "sys.cpu", Op: ">", Threshold: 90, Unit: "%", Duration: 60, Severity: "critical"})
	for _, want := range []string{"CPU (usage)", "sys.cpu", "JSON", "name", "description"} {
		if !contains(p, want) {
			t.Errorf("prompt missing %q: %s", want, p)
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
