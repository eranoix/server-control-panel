package fcmpush

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSendDataToUser_BuildsDataOnlyMessage(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := &Sender{
		store:   stubDeviceStore{tokens: []DeviceToken{{DeviceID: "d1", Token: "tok-1"}}},
		cred:    fakeCredential("test-project", "fake-token"),
		http:    srv.Client(),
		baseURL: srv.URL,
	}

	n := s.SendDataToUser(context.Background(), "sam", map[string]string{"type": "incoming-call", "room_id": "r1"}, DataOptions{}, nil)
	if n != 1 {
		t.Fatalf("delivered = %d, want 1", n)
	}

	msg, _ := gotBody["message"].(map[string]any)
	if msg == nil {
		t.Fatalf("body has no \"message\" key: %#v", gotBody)
	}
	if _, hasNotification := msg["notification"]; hasNotification {
		t.Fatalf("message must NOT carry a \"notification\" key, got: %#v", msg)
	}
	data, _ := msg["data"].(map[string]any)
	if data == nil || data["type"] != "incoming-call" || data["room_id"] != "r1" {
		t.Fatalf("message.data = %#v, want {type:incoming-call room_id:r1}", data)
	}
}

func TestSendDataToUser_SetsHighPriorityAndCollapseKey(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := &Sender{
		store:   stubDeviceStore{tokens: []DeviceToken{{DeviceID: "d1", Token: "tok-1"}}},
		cred:    fakeCredential("test-project", "fake-token"),
		http:    srv.Client(),
		baseURL: srv.URL,
	}

	n := s.SendDataToUser(context.Background(), "sam", map[string]string{"type": "incoming-call"}, DataOptions{CollapseKey: "call-room1", TTLSeconds: 30}, nil)
	if n != 1 {
		t.Fatalf("delivered = %d, want 1", n)
	}

	msg, _ := gotBody["message"].(map[string]any)
	android, _ := msg["android"].(map[string]any)
	if android == nil || android["priority"] != "high" {
		t.Fatalf("message.android.priority = %#v, want high", android["priority"])
	}
	if android["collapse_key"] != "call-room1" {
		t.Fatalf("message.android.collapse_key = %#v, want call-room1", android["collapse_key"])
	}
	if android["ttl"] != "30s" {
		t.Fatalf("message.android.ttl = %#v, want 30s", android["ttl"])
	}
}

func TestSendDataToUser_ReusesExistingTokenLookupAndAuth(t *testing.T) {
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := &Sender{
		store:   stubDeviceStore{tokens: []DeviceToken{{DeviceID: "d1", Token: "tok-1"}}},
		cred:    fakeCredential("test-project", "fake-token"),
		http:    srv.Client(),
		baseURL: srv.URL,
	}

	n := s.SendDataToUser(context.Background(), "sam", map[string]string{"type": "incoming-call"}, DataOptions{}, nil)
	if n != 1 {
		t.Fatalf("delivered = %d, want 1", n)
	}
	if gotAuth != "Bearer fake-token" {
		t.Fatalf("Authorization = %q, want %q — must reuse Sender's existing OAuth2 credential", gotAuth, "Bearer fake-token")
	}
	if gotPath == "" || !hasSuffixMessagesSend(gotPath, "test-project") {
		t.Fatalf("path = %q, want the same /v1/projects/test-project/messages:send URL SendToUser posts to", gotPath)
	}
}

func hasSuffixMessagesSend(path, projectID string) bool {
	want := "/v1/projects/" + projectID + "/messages:send"
	return len(path) >= len(want) && path[len(path)-len(want):] == want
}

func TestSendDataToUser_SkipsFilteredDevice(t *testing.T) {
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
	n := s.SendDataToAll(context.Background(), map[string]string{"type": "incoming-call"}, DataOptions{}, func(id string) bool { return id == "allowed" })
	if n != 1 {
		t.Fatalf("delivered = %d, want 1", n)
	}
	if calls != 1 {
		t.Fatalf("FCM endpoint hit %d times, want 1 (blocked device must never be posted)", calls)
	}
}

func TestSendDataToUser_NoTokens_NoCrash(t *testing.T) {
	s := &Sender{store: stubDeviceStore{}, cred: fakeCredential("p", "x"), http: http.DefaultClient}
	if n := s.SendDataToUser(context.Background(), "sam", map[string]string{"type": "incoming-call"}, DataOptions{}, nil); n != 0 {
		t.Fatalf("SendDataToUser with no tokens = %d, want 0", n)
	}
	if n := s.SendDataToAll(context.Background(), map[string]string{"type": "incoming-call"}, DataOptions{}, nil); n != 0 {
		t.Fatalf("SendDataToAll with no tokens = %d, want 0", n)
	}

	nilStore := &Sender{cred: fakeCredential("p", "x"), http: http.DefaultClient}
	if n := nilStore.SendDataToUser(context.Background(), "sam", map[string]string{}, DataOptions{}, nil); n != 0 {
		t.Fatalf("SendDataToUser with nil store = %d, want 0", n)
	}
}
