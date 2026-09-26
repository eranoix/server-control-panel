package fcmpush

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/oauth2"
)

type stubDeviceStore struct{ tokens []DeviceToken }

func (s stubDeviceStore) TokensForUser(string) []DeviceToken { return s.tokens }
func (s stubDeviceStore) AllTokens() []DeviceToken           { return s.tokens }

// fakeCredential builds a credential with a deterministic bearer token
// (oauth2.StaticTokenSource) — no real Google OAuth2 endpoint is ever
// contacted by these tests.
func fakeCredential(projectID, token string) *credential {
	return &credential{projectID: projectID, tokens: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})}
}

func TestFCMSender_SendToUser_PostsWellFormedMessageAndBearer(t *testing.T) {
	var gotAuth, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"name":"projects/test-project/messages/0"}`))
	}))
	defer srv.Close()

	s := &Sender{
		store:   stubDeviceStore{tokens: []DeviceToken{{DeviceID: "d1", Token: "tok-1"}}},
		cred:    fakeCredential("test-project", "fake-token"),
		http:    srv.Client(),
		baseURL: srv.URL,
	}

	n := s.SendToUser(context.Background(), "sam", []byte(`{"title":"oi","body":"tudo bem"}`), SendOptions{Priority: "high"}, nil)
	if n != 1 {
		t.Fatalf("delivered = %d, want 1", n)
	}
	if gotAuth != "Bearer fake-token" {
		t.Fatalf("Authorization = %q, want %q", gotAuth, "Bearer fake-token")
	}
	if !strings.HasSuffix(gotPath, "/v1/projects/test-project/messages:send") {
		t.Fatalf("path = %q, want suffix /v1/projects/test-project/messages:send", gotPath)
	}

	msg, _ := gotBody["message"].(map[string]any)
	if msg == nil {
		t.Fatalf("body has no \"message\" key: %#v", gotBody)
	}
	if msg["token"] != "tok-1" {
		t.Fatalf("message.token = %#v, want tok-1", msg["token"])
	}
	android, _ := msg["android"].(map[string]any)
	if android == nil || android["priority"] != "high" {
		t.Fatalf("message.android.priority = %#v, want high", android["priority"])
	}
	data, _ := msg["data"].(map[string]any)
	if data == nil || data["title"] != "oi" || data["body"] != "tudo bem" {
		t.Fatalf("message.data = %#v, want {title:oi body:tudo bem}", data)
	}
}

func TestFCMSender_AllowDeviceFiltersPerDevice(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := &Sender{
		store: stubDeviceStore{tokens: []DeviceToken{
			{DeviceID: "allowed", Token: "t1"},
			{DeviceID: "blocked", Token: "t2"},
		}},
		cred:    fakeCredential("p", "x"),
		http:    srv.Client(),
		baseURL: srv.URL,
	}
	n := s.SendToAll(context.Background(), []byte(`{"title":"x"}`), SendOptions{}, func(id string) bool { return id == "allowed" })
	if n != 1 {
		t.Fatalf("delivered = %d, want 1", n)
	}
	if calls != 1 {
		t.Fatalf("FCM endpoint hit %d times, want 1 (blocked device must never be posted)", calls)
	}
}

func TestFCMSender_NilStoreDegradesToZero(t *testing.T) {
	s := &Sender{cred: fakeCredential("p", "x"), http: http.DefaultClient}
	if n := s.SendToUser(context.Background(), "sam", []byte(`{}`), SendOptions{}, nil); n != 0 {
		t.Fatalf("SendToUser with nil store = %d, want 0", n)
	}
	if n := s.SendToAll(context.Background(), []byte(`{}`), SendOptions{}, nil); n != 0 {
		t.Fatalf("SendToAll with nil store = %d, want 0", n)
	}
}

func TestFCMSender_ExpiredTokenDoesNotCountAsDelivered(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	s := &Sender{
		store:   stubDeviceStore{tokens: []DeviceToken{{DeviceID: "gone", Token: "stale"}}},
		cred:    fakeCredential("p", "x"),
		http:    srv.Client(),
		baseURL: srv.URL,
	}
	if n := s.SendToUser(context.Background(), "sam", []byte(`{}`), SendOptions{}, nil); n != 0 {
		t.Fatalf("delivered = %d, want 0 for a 404/expired token", n)
	}
}
