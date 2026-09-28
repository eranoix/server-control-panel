package api

import (
	"encoding/json"
	"testing"
)

func TestPriceForModelOpus5(t *testing.T) {
	want := modelPrice{5, 25, 6.25, 10, 0.50}
	for _, model := range []string{"claude-opus-5[1m]", "claude-opus-5", "opus-5"} {
		if got := priceForModel(model); got != want {
			t.Errorf("priceForModel(%q) = %+v, want %+v", model, got, want)
		}
	}
	var matched bool
	for _, row := range modelPriceTable {
		if row.match == "opus-5" {
			matched = true
		}
	}
	if !matched {
		t.Error("modelPriceTable has no explicit entry for opus-5 (it would fall through to the silent default)")
	}
}

func TestPriceForModelOpus5NotLegacy(t *testing.T) {
	if got := priceForModel("claude-opus-5[1m]"); got.in == 15 {
		t.Errorf("Opus 5 matched a legacy price (%+v); table out of order", got)
	}
}

func TestCostForModelOpus5Empirical(t *testing.T) {
	got := costForModel("claude-opus-5[1m]", AgentTokens{
		In: 2, Out: 4, CacheCreation: 78592, CacheCreation1h: 78592,
	})
	const want = 0.78603
	if diff := got - want; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("costForModel = %v, want %v (the actual API invoice)", got, want)
	}
}

func TestCostForModelCacheTTLSplit(t *testing.T) {
	const cc = 100000
	base := 0.0
	cases := []struct {
		name string
		cc1h int64
		want float64
	}{
		{"all 5m (old transcript)", 0, base + cc/1e6*6.25},
		{"all 1h", cc, base + cc/1e6*10},
		{"half of each", cc / 2, base + (cc/2)/1e6*6.25 + (cc/2)/1e6*10},
	}
	for _, tc := range cases {
		got := costForModel("claude-opus-5", AgentTokens{CacheCreation: cc, CacheCreation1h: tc.cc1h})
		if diff := got - tc.want; diff > 1e-9 || diff < -1e-9 {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestCostForModelCacheTTLClamp(t *testing.T) {
	got := costForModel("claude-opus-5", AgentTokens{CacheCreation: 1000, CacheCreation1h: 999999})
	want := 1000.0 / 1e6 * 10
	if diff := got - want; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("clamp failed: got %v, want %v", got, want)
	}
	if neg := costForModel("claude-opus-5", AgentTokens{CacheCreation: 100, CacheCreation1h: -5}); neg < 0 {
		t.Errorf("negative cost with a negative 1h: %v", neg)
	}
}

func TestCcUsageCache1hParsing(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want int64
	}{
		{"pure 1h (real format)", `{"input_tokens":20478,"cache_creation_input_tokens":59972,"cache_read_input_tokens":0,"output_tokens":510,"cache_creation":{"ephemeral_1h_input_tokens":59972,"ephemeral_5m_input_tokens":0}}`, 59972},
		{"pure 5m", `{"cache_creation_input_tokens":1000,"cache_creation":{"ephemeral_1h_input_tokens":0,"ephemeral_5m_input_tokens":1000}}`, 0},
		{"mixed", `{"cache_creation_input_tokens":1000,"cache_creation":{"ephemeral_1h_input_tokens":400,"ephemeral_5m_input_tokens":600}}`, 400},
		{"no breakdown (old transcript)", `{"cache_creation_input_tokens":1000}`, 0},
		{"1h larger than the total (corrupted)", `{"cache_creation_input_tokens":10,"cache_creation":{"ephemeral_1h_input_tokens":999}}`, 10},
	}
	for _, tc := range cases {
		var u ccUsage
		if err := json.Unmarshal([]byte(tc.raw), &u); err != nil {
			t.Fatalf("%s: unmarshal: %v", tc.name, err)
		}
		if got := u.cache1h(); got != tc.want {
			t.Errorf("%s: cache1h() = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestPriceForModelOpus55(t *testing.T) {
	want := modelPrice{4, 20, 5, 8, 0.20}
	for _, model := range []string{"claude-opus-5-5[1m]", "claude-opus-5-5", "opus-5-5"} {
		if got := priceForModel(model); got != want {
			t.Errorf("priceForModel(%q) = %+v, want %+v", model, got, want)
		}
	}
	i55, i5 := -1, -1
	for i, row := range modelPriceTable {
		switch row.match {
		case "opus-5-5":
			i55 = i
		case "opus-5":
			i5 = i
		}
	}
	if i55 < 0 {
		t.Fatal("modelPriceTable has no explicit entry for opus-5-5")
	}
	if i5 >= 0 && i55 > i5 {
		t.Errorf("opus-5-5 (idx %d) comes AFTER opus-5 (idx %d): the substring matches first and charges the old rate", i55, i5)
	}
}

func TestCostForModelOpus55Empirical(t *testing.T) {
	got := costForModel("claude-opus-5-5[1m]", AgentTokens{
		In: 2, Out: 4, CacheCreation: 58850, CacheCreation1h: 58850,
	})
	const want = 0.470888
	if diff := got - want; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("costForModel = %v, want %v (the actual API invoice)", got, want)
	}
}
