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
		{`{"name":"CPU saturated","description":"Warns when the CPU goes above 90%."}`, "CPU saturated", "Warns when the CPU goes above 90%.", false},
		{"Sure! Here it is:\n```json\n{\"name\":\"High cost\",\"description\":\"Agent spend above the cap.\"}\n```", "High cost", "Agent spend above the cap.", false},
		{"no json here", "", "", true},
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
