package pve

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const (
	testTokenID = "panel@pve!t"
	testSecret  = "s3cr3t"
)

func newTestClient(t *testing.T, h http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := New(Config{BaseURL: srv.URL, TokenID: testTokenID, Secret: testSecret})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c, srv
}

func TestErrorClassification(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   Kind
	}{
		{"401 revoked", http.StatusUnauthorized, "authentication failure", KindNoCredential},
		{"403 without ACL", http.StatusForbidden, "Permission check failed (/vms/206, VM.Audit)", KindForbidden},
		{"500 hypervisor", http.StatusInternalServerError, "internal error", KindHypervisor},
		{"400 hypervisor", http.StatusBadRequest, "parameter verification failed", KindHypervisor},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			err := c.do(context.Background(), http.MethodGet, "/api2/json/version", nil)
			if err == nil {
				t.Fatalf("status %d returned a nil error", tc.status)
			}
			pe, ok := err.(*Error)
			if !ok {
				t.Fatalf("the error is not a *pve.Error: %T (%v)", err, err)
			}
			if pe.Kind != tc.want {
				t.Errorf("Kind = %v, want %v", pe.Kind, tc.want)
			}
			if pe.Status != tc.status {
				t.Errorf("Status = %d, want %d", pe.Status, tc.status)
			}
			if !strings.Contains(pe.Body, tc.body) {
				t.Errorf("Body = %q, want it to contain %q", pe.Body, tc.body)
			}
			if pe.Path != "/api2/json/version" {
				t.Errorf("Path = %q", pe.Path)
			}
		})
	}

	t.Run("dead transport", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		url := srv.URL
		srv.Close()
		c, err := New(Config{BaseURL: url, TokenID: testTokenID, Secret: testSecret})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		err = c.do(context.Background(), http.MethodGet, "/api2/json/version", nil)
		pe, ok := err.(*Error)
		if !ok {
			t.Fatalf("the error is not a *pve.Error: %T (%v)", err, err)
		}
		if pe.Kind != KindUnreachable {
			t.Errorf("Kind = %v, want KindUnreachable", pe.Kind)
		}
		if pe.Err == nil {
			t.Error("KindUnreachable with no wrapped Err — the diagnosis disappears")
		}
		if pe.Status != 0 {
			t.Errorf("Status = %d, want 0 (there was no response)", pe.Status)
		}
	})

	t.Run("distinct kinds", func(t *testing.T) {
		seen := map[Kind]string{}
		for _, k := range []Kind{KindOK, KindNoCredential, KindForbidden, KindUnreachable, KindHypervisor} {
			if old, ok := seen[k]; ok {
				t.Fatalf("duplicate Kind: %s == %s", k, old)
			}
			seen[k] = k.String()
		}
		if len(seen) != 5 {
			t.Fatalf("expected 5 distinct Kinds, saw %d", len(seen))
		}
	})
}

func TestAuthHeader(t *testing.T) {
	var seen http.Header
	var seenPath, seenMethod string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Clone()
		seenPath = r.URL.Path
		seenMethod = r.Method
		_, _ = w.Write([]byte(`{"data":{"version":"9.2.2"}}`))
	})

	var out struct {
		Version string `json:"version"`
	}
	if err := c.do(context.Background(), http.MethodGet, "/api2/json/version", &out); err != nil {
		t.Fatalf("do: %v", err)
	}
	if got, want := seen.Get("Authorization"), "PVEAPIToken=panel@pve!t=s3cr3t"; got != want {
		t.Errorf("Authorization = %q, want %q", got, want)
	}
	if v := seen.Get("CSRFPreventionToken"); v != "" {
		t.Errorf("CSRFPreventionToken present (%q) — an API token needs no CSRF", v)
	}
	if got := seen.Get("Accept"); got != "application/json" {
		t.Errorf("Accept = %q", got)
	}
	if seenPath != "/api2/json/version" || seenMethod != http.MethodGet {
		t.Errorf("request = %s %s", seenMethod, seenPath)
	}
	if out.Version != "9.2.2" {
		t.Errorf("the {\"data\":…} envelope was not unwrapped: %+v", out)
	}
}

func TestTimeoutFloor(t *testing.T) {
	cases := []struct {
		name string
		in   time.Duration
		want time.Duration
	}{
		{"zero becomes the floor", 0, minTimeout},
		{"short is RAISED to the floor", 2 * time.Second, minTimeout},
		{"generous is respected", 30 * time.Second, 30 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := New(Config{BaseURL: "http://127.0.0.1:1", TokenID: testTokenID, Secret: testSecret, Timeout: tc.in})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if c.httpc.Timeout != tc.want {
				t.Errorf("timeout = %v, want %v", c.httpc.Timeout, tc.want)
			}
		})
	}
	if minTimeout < 10*time.Second {
		t.Fatalf("minTimeout = %v — below the 10 s required by the 401's 3 s delay", minTimeout)
	}
}

func TestTokenFromVault(t *testing.T) {
	id, secret, err := SplitTokenValue("panel@pve!audit=1234-abcd")
	if err != nil {
		t.Fatalf("a valid value from the vault was refused: %v", err)
	}
	if id != "panel@pve!audit" || secret != "1234-abcd" {
		t.Fatalf("split = (%q,%q)", id, secret)
	}

	bad := []string{"", "1234-abcd", "panel@pve!audit", "panel@pve=1234", "=1234", "panel@pve!audit="}
	for _, v := range bad {
		if _, _, err := SplitTokenValue(v); err == nil {
			t.Errorf("SplitTokenValue(%q) accepted an invalid value", v)
		}
	}

	c, err := New(Config{BaseURL: "http://127.0.0.1:1", TokenID: "panel@pve!audit=1234-abcd"})
	if err != nil {
		t.Fatalf("New with the vault value: %v", err)
	}
	if c.tokenID != "panel@pve!audit" || c.secret != "1234-abcd" {
		t.Errorf("the client assembled (%q,…) — expected panel@pve!audit", c.tokenID)
	}
	if _, err := New(Config{BaseURL: "http://127.0.0.1:1", TokenID: "1234-abcd"}); err == nil {
		t.Error("New accepted a bare secret with no tokenid — that is the 07-01 defect")
	}
}

func TestErrorDoesNotLeakSecret(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("authentication failure"))
	})
	err := c.do(context.Background(), http.MethodGet, "/api2/json/version", nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), testSecret) {
		t.Fatalf("the secret leaked into the error text: %q", err.Error())
	}
}

func TestBodyTruncated(t *testing.T) {
	big := strings.Repeat("x", maxBodyBytes*2)
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(big))
	})
	err := c.do(context.Background(), http.MethodGet, "/api2/json/version", nil)
	pe, ok := err.(*Error)
	if !ok {
		t.Fatalf("the error is not a *pve.Error: %T", err)
	}
	if len(pe.Body) > maxBodyBytes {
		t.Fatalf("Body = %d bytes, the ceiling is %d", len(pe.Body), maxBodyBytes)
	}
}
