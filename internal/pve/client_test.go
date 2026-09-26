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
	testTokenID = "lab@pve!t"
	testSecret  = "s3cr3t"
)

// newTestClient brings up a fake hypervisor and returns a Client pointed at it.
// The server is plain HTTP on loopback (httptest); the HTTPS path with a pinned
// CA is exercised in tls_test.go.
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

// TestErrorClassification pins the rule that 401, 403, 5xx and a dead
// transport MUST become DIFFERENT Kinds. internal/jira/client.go:145 merges 401
// with 403 — here that would be a false green ("no credential" would show on
// screen when the problem is an ACL, and vice versa).
func TestErrorClassification(t *testing.T) {
	casos := []struct {
		nome   string
		status int
		corpo  string
		quer   Kind
	}{
		{"401 revogado", http.StatusUnauthorized, "authentication failure", KindNoCredential},
		{"403 sem ACL", http.StatusForbidden, "Permission check failed (/vms/206, VM.Audit)", KindForbidden},
		{"500 hipervisor", http.StatusInternalServerError, "internal error", KindHypervisor},
		{"400 hipervisor", http.StatusBadRequest, "parameter verification failed", KindHypervisor},
	}
	for _, tc := range casos {
		t.Run(tc.nome, func(t *testing.T) {
			c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.corpo))
			})
			err := c.do(context.Background(), http.MethodGet, "/api2/json/version", nil)
			if err == nil {
				t.Fatalf("status %d returned a nil error", tc.status)
			}
			pe, ok := err.(*Error)
			if !ok {
				t.Fatalf("the error is not a *pve.Error: %T (%v)", err, err)
			}
			if pe.Kind != tc.quer {
				t.Errorf("Kind = %v, want %v", pe.Kind, tc.quer)
			}
			if pe.Status != tc.status {
				t.Errorf("Status = %d, want %d", pe.Status, tc.status)
			}
			if !strings.Contains(pe.Body, tc.corpo) {
				t.Errorf("Body = %q, want it to contain %q", pe.Body, tc.corpo)
			}
			if pe.Path != "/api2/json/version" {
				t.Errorf("Path = %q", pe.Path)
			}
		})
	}

	// Unreachable: server closed BEFORE the call. This is the state that a short
	// timeout would make the hypervisor's 401 (delayed 3 s on purpose) imitate.
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

	// Antidote to the false green: a Kind is only useful if all four are distinct
	// from one another. If someone collapses two of them into the same value, this
	// fails.
	t.Run("distinct kinds", func(t *testing.T) {
		vistos := map[Kind]string{}
		for _, k := range []Kind{KindOK, KindNoCredential, KindForbidden, KindUnreachable, KindHypervisor} {
			if antigo, ok := vistos[k]; ok {
				t.Fatalf("duplicate Kind: %s == %s", k, antigo)
			}
			vistos[k] = k.String()
		}
		if len(vistos) != 5 {
			t.Fatalf("expected 5 distinct Kinds, saw %d", len(vistos))
		}
	})
}

// TestAuthHeader asserts the header byte by byte. The hypervisor requires
// "PVEAPIToken=USER@REALM!ID=SEGREDO" with no space, and a token does not need
// CSRFPreventionToken (HTTPServer.pm:122-129).
func TestAuthHeader(t *testing.T) {
	var visto http.Header
	var vistoPath, vistoMetodo string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		visto = r.Header.Clone()
		vistoPath = r.URL.Path
		vistoMetodo = r.Method
		_, _ = w.Write([]byte(`{"data":{"version":"9.2.2"}}`))
	})

	var out struct {
		Version string `json:"version"`
	}
	if err := c.do(context.Background(), http.MethodGet, "/api2/json/version", &out); err != nil {
		t.Fatalf("do: %v", err)
	}
	if got, quer := visto.Get("Authorization"), "PVEAPIToken=lab@pve!t=s3cr3t"; got != quer {
		t.Errorf("Authorization = %q, want %q", got, quer)
	}
	if v := visto.Get("CSRFPreventionToken"); v != "" {
		t.Errorf("CSRFPreventionToken present (%q) — an API token needs no CSRF", v)
	}
	if got := visto.Get("Accept"); got != "application/json" {
		t.Errorf("Accept = %q", got)
	}
	if vistoPath != "/api2/json/version" || vistoMetodo != http.MethodGet {
		t.Errorf("request = %s %s", vistoMetodo, vistoPath)
	}
	if out.Version != "9.2.2" {
		t.Errorf("the {\"data\":…} envelope was not unwrapped: %+v", out)
	}
}

// TestTimeoutFloor: 10 s is a FLOOR, not a default. The hypervisor holds every
// 401 response for 3 s on purpose (measured at 3.079 s) — with a smaller
// timeout, a revoked token turns into "unreachable" and the classification goes
// false green.
func TestTimeoutFloor(t *testing.T) {
	casos := []struct {
		nome string
		in   time.Duration
		quer time.Duration
	}{
		{"zero vira o piso", 0, minTimeout},
		{"curto é ELEVADO ao piso", 2 * time.Second, minTimeout},
		{"folgado é respeitado", 30 * time.Second, 30 * time.Second},
	}
	for _, tc := range casos {
		t.Run(tc.nome, func(t *testing.T) {
			c, err := New(Config{BaseURL: "http://127.0.0.1:1", TokenID: testTokenID, Secret: testSecret, Timeout: tc.in})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if c.httpc.Timeout != tc.quer {
				t.Errorf("timeout = %v, want %v", c.httpc.Timeout, tc.quer)
			}
		})
	}
	if minTimeout < 10*time.Second {
		t.Fatalf("minTimeout = %v — below the 10 s required by the 401's 3 s delay", minTimeout)
	}
}

// TestTokenDoCofre: the vault keeps "<tokenid>=<secret>" in a single string —
// that was the real false green (every offline pin green, 401 on the first live
// call). A bare secret, with no "!" in the id, MUST become an error in New,
// never a silently broken header.
func TestTokenDoCofre(t *testing.T) {
	id, seg, err := SplitTokenValue("lab@pve!audit=1234-abcd")
	if err != nil {
		t.Fatalf("a valid value from the vault was refused: %v", err)
	}
	if id != "lab@pve!audit" || seg != "1234-abcd" {
		t.Fatalf("split = (%q,%q)", id, seg)
	}

	ruins := []string{"", "1234-abcd", "lab@pve!audit", "lab@pve=1234", "=1234", "lab@pve!audit="}
	for _, v := range ruins {
		if _, _, err := SplitTokenValue(v); err == nil {
			t.Errorf("SplitTokenValue(%q) accepted an invalid value", v)
		}
	}

	// New accepts the whole vault value in TokenID, with Secret empty.
	c, err := New(Config{BaseURL: "http://127.0.0.1:1", TokenID: "lab@pve!audit=1234-abcd"})
	if err != nil {
		t.Fatalf("New with the vault value: %v", err)
	}
	if c.tokenID != "lab@pve!audit" || c.secret != "1234-abcd" {
		t.Errorf("the client assembled (%q,…) — expected lab@pve!audit", c.tokenID)
	}
	if _, err := New(Config{BaseURL: "http://127.0.0.1:1", TokenID: "1234-abcd"}); err == nil {
		t.Error("New accepted a bare secret with no tokenid — that is the 07-01 defect")
	}
}

// TestErrorNaoVazaSegredo: the error text is read in the log and on screen. The
// body comes from the SERVER; the auth header never goes in.
func TestErrorNaoVazaSegredo(t *testing.T) {
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

// TestBodyTruncado: a giant body from the hypervisor does not become a giant
// error in the log.
func TestBodyTruncado(t *testing.T) {
	grande := strings.Repeat("x", maxBodyBytes*2)
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(grande))
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
