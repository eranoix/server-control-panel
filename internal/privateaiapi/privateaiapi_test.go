package privateaiapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestClient spins up a fake admin API and returns a Client pointed at it.
func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return New(srv.URL, "test-admin-token")
}

func TestNewDefaultsBaseURL(t *testing.T) {
	c := New("", "tok")
	if c.BaseURL != DefaultBaseURL {
		t.Fatalf("empty baseURL should default to %q, got %q", DefaultBaseURL, c.BaseURL)
	}
	if New("http://x:1/", "tok").BaseURL != "http://x:1" {
		t.Fatalf("trailing slash should be trimmed")
	}
}

func TestListKeysPassesThroughAndAuthenticates(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-admin-token" {
			t.Errorf("missing/wrong auth header: %q", got)
		}
		if r.URL.Path != "/admin/api/keys" || r.Method != http.MethodGet {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"data":[{"id":1,"name":"k"}]}`))
	})

	res, err := c.ListKeys(context.Background())
	if err != nil {
		t.Fatalf("ListKeys: %v", err)
	}
	if res.Status != 200 {
		t.Fatalf("status = %d, want 200", res.Status)
	}
	if !strings.Contains(string(res.Body), `"name":"k"`) {
		t.Fatalf("body not passed through: %s", res.Body)
	}
}

func TestCreateKeyForwardsBodyAndStatus(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var got map[string]any
		_ = json.NewDecoder(r.Body).Decode(&got)
		if got["name"] != "ci" {
			t.Errorf("name not forwarded: %v", got)
		}
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"plaintext":"sk-secret","row":{"id":9,"name":"ci"}}`))
	})

	res, err := c.CreateKey(context.Background(), map[string]any{"name": "ci"})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}
	if res.Status != 201 {
		t.Fatalf("status = %d, want 201", res.Status)
	}
	if !strings.Contains(string(res.Body), "sk-secret") {
		t.Fatalf("plaintext not passed through: %s", res.Body)
	}
}

func TestRevokeAndUpdateBuildCorrectPaths(t *testing.T) {
	var seen []string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	if _, err := c.RevokeKey(context.Background(), 7); err != nil {
		t.Fatalf("RevokeKey: %v", err)
	}
	if _, err := c.UpdateKey(context.Background(), 7, map[string]any{"rateLimitRpm": 10}); err != nil {
		t.Fatalf("UpdateKey: %v", err)
	}
	want := []string{"POST /admin/api/keys/7/revoke", "PATCH /admin/api/keys/7"}
	for i, w := range want {
		if i >= len(seen) || seen[i] != w {
			t.Fatalf("requests = %v, want %v", seen, want)
		}
	}
}

func TestStatusFansInAndDegradesGracefully(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/admin/api/oauth-status"):
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"loggedIn":true}`))
		case strings.HasPrefix(r.URL.Path, "/admin/api/system"):
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"version":"0.2.0"}`))
		case strings.HasPrefix(r.URL.Path, "/admin/api/stats"):
			w.WriteHeader(500) // simulate a failing sub-call
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	})

	st := c.Status(context.Background())
	if st["oauth"] == nil || st["system"] == nil {
		t.Fatalf("oauth/system should be populated: %v", st)
	}
	if st["stats"] != nil {
		t.Fatalf("failing stats should be nil, got %v", st["stats"])
	}
	errs, ok := st["errors"].(map[string]string)
	if !ok || errs["stats"] == "" {
		t.Fatalf("errors map should record stats failure: %v", st["errors"])
	}
}
