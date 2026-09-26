package api

import (
	"encoding/json"
	"testing"
)

// TestPriceForModelOpus5 pins the price of Opus 5 and, above all, that it is
// matched EXPLICITLY in the table instead of falling through to
// defaultModelPrice. The default returns the same numbers today, so a missing
// match is invisible in the value — it only shows up the day the default
// changes. That is why the test checks the table.
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

// TestPriceForModelOpus5NotLegacy makes sure the table's order does not let
// Opus 5 match a legacy row. "opus-5" does not contain "opus-4"/"opus-3", but
// the table matches by substring and reordering is easy — this test is the net.
func TestPriceForModelOpus5NotLegacy(t *testing.T) {
	if got := priceForModel("claude-opus-5[1m]"); got.in == 15 {
		t.Errorf("Opus 5 matched a legacy price (%+v); table out of order", got)
	}
}

// TestCostForModelOpus5Empirical reproduces the REAL bill measured on a one-shot
// call to Opus 5: in=2, out=4, cache_creation=78592 all on the 1h TTL, and the
// API returned total_cost_usd 0.78603. This is the anchor test — with the 5m
// rate (6.25) the result came out at 0.49, ~1.6× below the bill.
func TestCostForModelOpus5Empirical(t *testing.T) {
	got := costForModel("claude-opus-5[1m]", AgentTokens{
		In: 2, Out: 4, CacheCreation: 78592, CacheCreation1h: 78592,
	})
	const want = 0.78603 // the literal value returned by the API
	if diff := got - want; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("costForModel = %v, want %v (the actual API invoice)", got, want)
	}
}

// TestCostForModelCacheTTLSplit covers the three TTL regimes on a single model:
// all 5m, all 1h, and half and half. The "all 5m" case pins compatibility with
// old transcripts (no breakdown → CacheCreation1h zero).
func TestCostForModelCacheTTLSplit(t *testing.T) {
	const cc = 100000
	base := 0.0 // no in/out, to isolate the cache-write effect
	cases := []struct {
		name string
		cc1h int64
		want float64
	}{
		{"tudo 5m (transcript antigo)", 0, base + cc/1e6*6.25},
		{"tudo 1h", cc, base + cc/1e6*10},
		{"metade de cada", cc / 2, base + (cc/2)/1e6*6.25 + (cc/2)/1e6*10},
	}
	for _, tc := range cases {
		got := costForModel("claude-opus-5", AgentTokens{CacheCreation: cc, CacheCreation1h: tc.cc1h})
		if diff := got - tc.want; diff > 1e-9 || diff < -1e-9 {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestCostForModelCacheTTLClamp makes sure a corrupted record (1h greater than
// the total) neither charges a negative amount on the 5m side nor blows the bill
// up: the excess is clamped to the total, never added on top.
func TestCostForModelCacheTTLClamp(t *testing.T) {
	got := costForModel("claude-opus-5", AgentTokens{CacheCreation: 1000, CacheCreation1h: 999999})
	want := 1000.0 / 1e6 * 10 // everything treated as 1h, capped at the total
	if diff := got - want; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("clamp failed: got %v, want %v", got, want)
	}
	if neg := costForModel("claude-opus-5", AgentTokens{CacheCreation: 100, CacheCreation1h: -5}); neg < 0 {
		t.Errorf("negative cost with a negative 1h: %v", neg)
	}
}

// TestCcUsageCache1hParsing validates the parsing of real JSONL — the exact
// format observed in a real transcript — including the line WITHOUT the
// cache_creation object (an old transcript), which must return zero instead of
// blowing up.
func TestCcUsageCache1hParsing(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want int64
	}{
		{"1h puro (formato real)", `{"input_tokens":20478,"cache_creation_input_tokens":59972,"cache_read_input_tokens":0,"output_tokens":510,"cache_creation":{"ephemeral_1h_input_tokens":59972,"ephemeral_5m_input_tokens":0}}`, 59972},
		{"5m puro", `{"cache_creation_input_tokens":1000,"cache_creation":{"ephemeral_1h_input_tokens":0,"ephemeral_5m_input_tokens":1000}}`, 0},
		{"misto", `{"cache_creation_input_tokens":1000,"cache_creation":{"ephemeral_1h_input_tokens":400,"ephemeral_5m_input_tokens":600}}`, 400},
		{"sem breakdown (transcript antigo)", `{"cache_creation_input_tokens":1000}`, 0},
		{"1h maior que o total (corrompido)", `{"cache_creation_input_tokens":10,"cache_creation":{"ephemeral_1h_input_tokens":999}}`, 10},
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

// TestPriceForModelOpus55 trava o preço do Opus 5.5 e — o ponto crítico — que
// ele NÃO cai na linha do Opus 5. A tabela casa por substring e "claude-opus-5-5"
// contém "opus-5": se a linha nova ficar depois da antiga, a sessão passa a ser
// cobrada a $5/$25 em vez de $4/$20 (e o cache read a 0.50 em vez de 0.20), um
// erro silencioso de ~25% no token e 2,5× no cache.
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
		t.Fatal("modelPriceTable não tem entrada explícita para opus-5-5")
	}
	if i5 >= 0 && i55 > i5 {
		t.Errorf("opus-5-5 (idx %d) vem DEPOIS de opus-5 (idx %d): o substring casa antes e cobra a taxa antiga", i55, i5)
	}
}

// TestCostForModelOpus55Empirical reproduz a fatura REAL medida numa chamada
// one-shot ao Opus 5.5 pelo router deste host: in=2, out=4, cache_creation=58850
// todo em TTL de 1h, e a API devolveu costUSD 0.470888. É o teste-âncora das
// taxas novas — com as do Opus 5 daria 0.589088, ~25% acima da fatura.
func TestCostForModelOpus55Empirical(t *testing.T) {
	got := costForModel("claude-opus-5-5[1m]", AgentTokens{
		In: 2, Out: 4, CacheCreation: 58850, CacheCreation1h: 58850,
	})
	const want = 0.470888 // valor literal devolvido pela API
	if diff := got - want; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("costForModel = %v, want %v (the actual API invoice)", got, want)
	}
}
