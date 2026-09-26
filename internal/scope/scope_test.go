package scope

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestNew_Valid(t *testing.T) {
	cases := []string{
		"sam",
		"jordan",
		"a",
		"a_b",
		"A-B-C",
		"user_2026",
		"ARTHUR",
		"x0",
		strings.Repeat("a", MaxUsernameLen),
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			u, err := New(raw)
			if err != nil {
				t.Fatalf("New(%q) = err %v, want valid", raw, err)
			}
			if u.String() != raw {
				t.Fatalf("u.String() = %q, want %q", u.String(), raw)
			}
			if !u.Valid() {
				t.Fatalf("u.Valid() = false for %q", raw)
			}
		})
	}
}

func TestNew_Malicious(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want error
	}{
		{"empty", "", ErrEmpty},
		{"path-traversal", "../etc/passwd", ErrInvalidChar},
		{"double-dot", "..", ErrInvalidChar},
		{"single-dot", ".", ErrInvalidChar},
		{"absolute-path", "/etc/shadow", ErrInvalidChar},
		{"slash-middle", "a/b", ErrInvalidChar},
		{"backslash", "a\\b", ErrInvalidChar},
		{"null-byte", "a\x00b", ErrInvalidChar},
		{"space", "a b", ErrInvalidChar},
		{"dot-middle", "a.b", ErrInvalidChar},
		{"dot-prefix", ".a", ErrInvalidChar},
		{"dot-suffix", "a.", ErrInvalidChar},
		{"colon", "a:b", ErrInvalidChar},
		{"newline", "a\nb", ErrInvalidChar},
		{"tab", "a\tb", ErrInvalidChar},
		{"unicode", "árthur", ErrInvalidChar},
		{"emoji", "user🦀", ErrInvalidChar},
		{"too-long", strings.Repeat("a", MaxUsernameLen+1), ErrTooLong},
		{"way-too-long", strings.Repeat("a", 1000), ErrTooLong},
		{"reserved-system", "system", ErrReserved},
		{"reserved-system-upper", "SYSTEM", ErrReserved},
		{"reserved-anon", "anon", ErrReserved},
		{"reserved-archive", "archive", ErrReserved},
		{"reserved-root", "root", ErrReserved},
		{"trailing-slash", "a/", ErrInvalidChar},
		{"leading-slash", "/a", ErrInvalidChar},
		{"semicolon", "a;b", ErrInvalidChar},
		{"pipe", "a|b", ErrInvalidChar},
		{"ampersand", "a&b", ErrInvalidChar},
		{"dollar", "a$b", ErrInvalidChar},
		{"backtick", "a`b", ErrInvalidChar},
		{"question", "a?b", ErrInvalidChar},
		{"asterisk", "a*b", ErrInvalidChar},
		{"angle-bracket", "a>b", ErrInvalidChar},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u, err := New(tc.raw)
			if err == nil {
				t.Fatalf("New(%q) = %q nil, want err %v", tc.raw, u, tc.want)
			}
			if err != tc.want {
				t.Fatalf("New(%q) err = %v, want %v", tc.raw, err, tc.want)
			}
		})
	}
}

func TestPaths_NoTraversalFromDataDir(t *testing.T) {
	dataDir := "/opt/panel/data"
	users := []string{"sam", "jordan", "a", "A_b-c"}
	for _, raw := range users {
		u := MustNew(raw)
		p := PathsFor(dataDir, u)

		checks := map[string]string{
			"Root":     p.Root,
			"Whatsapp": p.Whatsapp,
			"Uploads":  p.Uploads,
			"Browser":  p.Browser,
		}
		for name, full := range checks {
			rel, err := filepath.Rel(dataDir, full)
			if err != nil {
				t.Fatalf("user=%q %s: Rel failed: %v", raw, name, err)
			}
			if strings.HasPrefix(rel, "..") || strings.Contains(rel, "../") {
				t.Fatalf("user=%q %s: escapes dataDir: rel=%q", raw, name, rel)
			}
			wantPrefix := filepath.Join("users", raw)
			if !strings.HasPrefix(rel, wantPrefix) {
				t.Fatalf("user=%q %s: rel=%q does not start with %q",
					raw, name, rel, wantPrefix)
			}
		}
	}
}

func TestPaths_WhatsappContainerOutsideDataDir(t *testing.T) {
	u := MustNew("sam")
	p := PathsFor("/opt/panel/data", u)
	wantPrefix := "/var/lib/vpsm-whatsapp/sam"
	if !strings.HasPrefix(p.WhatsappContainer, wantPrefix) {
		t.Fatalf("WhatsappContainer = %q, want prefix %q",
			p.WhatsappContainer, wantPrefix)
	}
	if !strings.HasPrefix(p.WhatsappMedia, wantPrefix+"/media") {
		t.Fatalf("WhatsappMedia = %q, want under %q/media",
			p.WhatsappMedia, wantPrefix)
	}
	if p.AIEnv != "/etc/claude-router/users/sam.env" {
		t.Fatalf("AIEnv = %q, want /etc/claude-router/users/sam.env",
			p.AIEnv)
	}
}

func TestMustNew_PanicsOnInvalid(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("MustNew(\"../foo\") did not panic")
		}
	}()
	MustNew("../foo")
}

func TestIsGlobalKey(t *testing.T) {
	if !IsGlobalKey("JWT_SECRET") {
		t.Fatal("JWT_SECRET should be global")
	}
	if IsGlobalKey("waha_api_key") {
		t.Fatal("waha_api_key should NOT be global")
	}
	if IsGlobalKey("") {
		t.Fatal("empty key should not be reported as global")
	}
}

func TestValidLogicalKey(t *testing.T) {
	good := []string{"waha_api_key", "waha_hmac_secret", "x", "a.b.c"}
	for _, k := range good {
		if !validLogicalKey(k) {
			t.Fatalf("validLogicalKey(%q) = false, want true", k)
		}
	}
	bad := []string{"", "a:b", "x\x00y"}
	for _, k := range bad {
		if validLogicalKey(k) {
			t.Fatalf("validLogicalKey(%q) = true, want false", k)
		}
	}
}
