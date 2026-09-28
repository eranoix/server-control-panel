package pve

import (
	"context"
	"go/parser"
	"go/token"
	"net/http"
	"strconv"
	"testing"
)

func TestListTokens(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if want := "/api2/json/access/users/panel@pve/token"; r.URL.Path != want {
			t.Errorf("path = %q, want %q", r.URL.Path, want)
		}
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		_, _ = w.Write([]byte(`{"data":[
			{"tokenid":"audit","privsep":1,"expire":1802000000,"comment":"discovery"},
			{"tokenid":"admin","privsep":0,"expire":0}
		]}`))
	})
	toks, err := c.ListTokens(context.Background(), "panel@pve")
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

func TestTokenInfo(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if want := "/api2/json/access/users/panel@pve/token/node-apps"; r.URL.Path != want {
			t.Errorf("path = %q, want %q", r.URL.Path, want)
		}
		_, _ = w.Write([]byte(`{"data":{"privsep":1,"expire":1802000000,"comment":"no apps"}}`))
	})
	info, err := c.TokenInfo(context.Background(), "panel@pve", "node-apps")
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

func TestExpiresIn(t *testing.T) {
	const now = 1800000000
	cases := []struct {
		name    string
		expire  int64
		days    int
		expires bool
	}{
		{"never expires", 0, 0, false},
		{"expires in 30 days", now + 30*86400, 30, true},
		{"already expired (exactly 1 day)", now - 86400, -1, true},
		{"expired one hour ago", now - 3600, -1, true},
		{"expires in 12 hours", now + 43200, 0, true},
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

func TestDeleteToken(t *testing.T) {
	var seen string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		seen = r.Method + " " + r.URL.Path
		_, _ = w.Write([]byte(`{"data":null}`))
	})
	if err := c.DeleteToken(context.Background(), "panel@pve", "node-apps"); err != nil {
		t.Fatalf("DeleteToken: %v", err)
	}
	if want := "DELETE /api2/json/access/users/panel@pve/token/node-apps"; seen != want {
		t.Fatalf("call = %q, want %q", seen, want)
	}
}

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
				_, _ = w.Write([]byte("pve error"))
			})
			err := c.DeleteToken(context.Background(), "panel@pve", "x")
			pe, ok := err.(*Error)
			if !ok || pe.Kind != tc.want {
				t.Fatalf("status %d → %v (%T), want %v", tc.status, err, err, tc.want)
			}
		})
	}
}

func TestTokenIDInvalid(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("called the hypervisor: %s %s", r.Method, r.URL.Path)
	})
	for _, tc := range []struct{ user, tok string }{
		{"", "audit"},
		{"panel@pve", ""},
		{"panel@pve", "a/b"},
		{"panel@pve", "../../access/users"},
		{"lab", "audit"},
	} {
		if err := c.DeleteToken(context.Background(), tc.user, tc.tok); err == nil {
			t.Errorf("DeleteToken(%q,%q) was accepted", tc.user, tc.tok)
		}
	}
}

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
