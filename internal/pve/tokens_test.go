package pve

import (
	"context"
	"go/parser"
	"go/token"
	"net/http"
	"strconv"
	"testing"
)

// TestListTokens proves the parse of the two fields the panel must NOT lose:
// privsep (the hypervisor sends a numeric 0/1, not a JSON boolean) and expire.
//
// 🔴 expire is the trap: verify_token does `die "access expired"` when
// expire < time(), and that turns into a 401 — INDISTINGUISHABLE from
// revocation. The tokens of this house do have an expiry date. Without storing
// it, N months from now the panel will say "no credential" and nobody will know
// why.
func TestListTokens(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if want := "/api2/json/access/users/lab@pve/token"; r.URL.Path != want {
			t.Errorf("path = %q, want %q", r.URL.Path, want)
		}
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		_, _ = w.Write([]byte(`{"data":[
			{"tokenid":"audit","privsep":1,"expire":1802000000,"comment":"descoberta"},
			{"tokenid":"admin","privsep":0,"expire":0}
		]}`))
	})
	toks, err := c.ListTokens(context.Background(), "lab@pve")
	if err != nil {
		t.Fatalf("ListTokens: %v", err)
	}
	if len(toks) != 2 {
		t.Fatalf("toks = %+v, want 2", toks)
	}
	if !toks[0].Privsep {
		t.Errorf("privsep 1 did not become true: %+v", toks[0])
	}
	if toks[0].Expire != 1802000000 {
		t.Errorf("expire = %d, want 1802000000", toks[0].Expire)
	}
	if toks[1].Privsep {
		t.Errorf("privsep 0 did not become false: %+v", toks[1])
	}
	if toks[1].Expire != 0 {
		t.Errorf("expire = %d, want 0 (never expires)", toks[1].Expire)
	}
}

// TestTokenInfo: the GET of a single token returns the object WITHOUT the
// tokenid inside it (the id is in the path). Whoever calls already knows which
// one they asked for — returning the field empty would force the handler above
// to patch around it.
func TestTokenInfo(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if want := "/api2/json/access/users/lab@pve/token/node-apps"; r.URL.Path != want {
			t.Errorf("path = %q, want %q", r.URL.Path, want)
		}
		_, _ = w.Write([]byte(`{"data":{"privsep":1,"expire":1802000000,"comment":"no apps"}}`))
	})
	info, err := c.TokenInfo(context.Background(), "lab@pve", "node-apps")
	if err != nil {
		t.Fatalf("TokenInfo: %v", err)
	}
	if info.TokenID != "node-apps" {
		t.Errorf("tokenid = %q, want the one from the path", info.TokenID)
	}
	if !info.Privsep || info.Expire != 1802000000 {
		t.Errorf("info = %+v", info)
	}
}

// TestExpiresIn proves the calculation the screen shows ("expires in N days")
// with an INJECTED clock — a criterion that is met by waiting is forbidden.
func TestExpiresIn(t *testing.T) {
	const now = 1800000000
	cases := []struct {
		name    string
		expire  int64
		days    int
		expires bool
	}{
		{"nunca vence", 0, 0, false},
		{"vence em 30 dias", now + 30*86400, 30, true},
		{"ja venceu (1 dia cravado)", now - 86400, -1, true},
		// 🔴 Expired ONE HOUR ago. Integer division in Go truncates towards zero:
		// -3600/86400 == 0, and the screen would say "expires today" for a credential
		// that is ALREADY returning 401. Only a remainder that is not a multiple of
		// 86400 separates truncating from rounding down.
		{"venceu ha uma hora", now - 3600, -1, true},
		{"vence em 12 horas", now + 43200, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			info := TokenInfo{Expire: tc.expire}
			days, expires := info.ExpiresInDays(now)
			if expires != tc.expires || (tc.expires && days != tc.days) {
				t.Fatalf("ExpiresInDays = (%d, %v), want (%d, %v)", days, expires, tc.days, tc.expires)
			}
		})
	}
}

// TestDeleteToken is the hypervisor half of revocation. The handler above
// ORDERS it: first DELETE on the hypervisor, then confirm the 401, and only
// then erase it from the vault — if the order were inverted, a clean vault with
// a live token on the hypervisor would be an orphan credential nobody can
// revoke any more.
func TestDeleteToken(t *testing.T) {
	var seen string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		seen = r.Method + " " + r.URL.Path
		_, _ = w.Write([]byte(`{"data":null}`))
	})
	if err := c.DeleteToken(context.Background(), "lab@pve", "node-apps"); err != nil {
		t.Fatalf("DeleteToken: %v", err)
	}
	if want := "DELETE /api2/json/access/users/lab@pve/token/node-apps"; seen != want {
		t.Fatalf("call = %q, want %q", seen, want)
	}
}

// TestDeleteTokenSeparateKinds: 401 and 403 must NOT collapse into the same
// error. "the token I use to revoke has itself been revoked" (401) and "that
// token has no permission to revoke" (403) ask opposite actions of the
// operator.
func TestDeleteTokenSeparateKinds(t *testing.T) {
	cases := []struct {
		status int
		want   Kind
	}{
		{http.StatusUnauthorized, KindNoCredential},
		{http.StatusForbidden, KindForbidden},
		{http.StatusInternalServerError, KindHypervisor},
	}
	for _, tc := range cases {
		t.Run(tc.want.String(), func(t *testing.T) {
			c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte("erro do pve"))
			})
			err := c.DeleteToken(context.Background(), "lab@pve", "x")
			pe, ok := err.(*Error)
			if !ok || pe.Kind != tc.want {
				t.Fatalf("status %d → %v (%T), want %v", tc.status, err, err, tc.want)
			}
		})
	}
}

// TestTokenIDInvalid: an empty user, or a token containing "/", would build a
// different path on the hypervisor. A DELETE against the wrong path is the
// worst class of bug there is here.
func TestTokenIDInvalid(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("called the hypervisor: %s %s", r.Method, r.URL.Path)
	})
	for _, tc := range []struct{ user, tok string }{
		{"", "audit"},
		{"lab@pve", ""},
		{"lab@pve", "a/b"},
		{"lab@pve", "../../access/users"},
		{"lab", "audit"}, // no realm
	} {
		if err := c.DeleteToken(context.Background(), tc.user, tc.tok); err == nil {
			t.Errorf("DeleteToken(%q,%q) was accepted", tc.user, tc.tok)
		}
	}
}

// 🔴 TestPveDoesNotImportVault pins the layer separation: internal/pve talks to
// the hypervisor and NEVER to the vault. What joins the two halves is the
// handler above, which is also what ORDERS the revocation (hypervisor first,
// vault afterwards). If this package started reading the vault on its own, the
// order would stop being verifiable in one single place.
//
// The reading is of the REAL import block (go/parser), not of a substring: the
// first version of this pin used strings.Contains and failed itself, because
// the forbidden path appears in this very comment. A substring does not tell an
// import from a mention — and a pin that bites its own text teaches people to
// switch it off.
func TestPveDoesNotImportVault(t *testing.T) {
	forbiddenBins := map[string]bool{
		"server-control-panel/internal/secrets": true,
		"server-control-panel/internal/scope":   true,
	}
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parsing the package: %v", err)
	}
	seen := 0
	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			seen++
			for _, imp := range file.Imports {
				path, err := strconv.Unquote(imp.Path.Value)
				if err != nil {
					t.Fatalf("%s: unreadable import %s", name, imp.Path.Value)
				}
				if forbiddenBins[path] {
					t.Errorf("%s imports %s — internal/pve cannot reach the vault", name, path)
				}
			}
		}
	}
	if seen == 0 {
		t.Fatal("no .go file scanned — the guard would be green by absence")
	}
}
