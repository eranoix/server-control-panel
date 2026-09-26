package mobilebff

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/videocall"
)

// fakeVideocallRoomLister is a double of *videocall.Service — it tests
// exactly the boundary this package depends on (videocallRoomLister), without
// having to open a real Service (flush/heartbeat goroutines + disk).
type fakeVideocallRoomLister struct {
	byUser map[string][]videocall.Room
}

func (f *fakeVideocallRoomLister) ListForUser(user string) []videocall.Room {
	return f.byUser[user]
}

func newVideocallTestAPI(svc videocallRoomLister) (huma.API, *http.ServeMux) {
	mux := http.NewServeMux()
	api := humago.NewWithPrefix(mux, Prefix, huma.DefaultConfig("test", "0.0"))
	registerVideocallWithService(api, svc)
	return api, mux
}

func TestHandleVideocallRooms_ReturnsOnlyCallerRooms(t *testing.T) {
	svc := &fakeVideocallRoomLister{byUser: map[string][]videocall.Room{
		"sam": {
			{ID: "r1", Name: "Sala 1", Owner: "sam", Members: []string{"jordan"}, CreatedAt: 100},
		},
	}}
	_, mux := newVideocallTestAPI(svc)

	req := newAuthedRequest(http.MethodGet, "/api/mobile/v1/videocall/rooms", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got []videocall.Room
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
	}
	if len(got) != 1 || got[0].ID != "r1" || got[0].Owner != "sam" {
		t.Fatalf("got = %+v, want [{ID:r1 Owner:sam ...}]", got)
	}
}

func TestHandleVideocallRooms_EmptyForUserWithNoRooms(t *testing.T) {
	svc := &fakeVideocallRoomLister{byUser: map[string][]videocall.Room{}}
	_, mux := newVideocallTestAPI(svc)

	req := newAuthedRequest(http.MethodGet, "/api/mobile/v1/videocall/rooms", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got []videocall.Room
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
	}
	if len(got) != 0 {
		t.Fatalf("got = %+v, want empty", got)
	}
}

func TestHandleVideocallRooms_NilServiceDegradesToEmpty(t *testing.T) {
	_, mux := newVideocallTestAPI(nil)

	req := newAuthedRequest(http.MethodGet, "/api/mobile/v1/videocall/rooms", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got []videocall.Room
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
	}
	if len(got) != 0 {
		t.Fatalf("got = %+v, want empty (nil service must not panic)", got)
	}
}

func TestHandleVideocallRooms_Unauthenticated401(t *testing.T) {
	svc := &fakeVideocallRoomLister{}
	_, mux := newVideocallTestAPI(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/mobile/v1/videocall/rooms", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body=%s)", rec.Code, rec.Body.String())
	}
}

// TestVideocallWSTicket_AuthenticatedUser_IssuesTicket proves the Android
// data layer has a BFF route to mint a /ws/videocall ticket
// through — it is forbidden from calling /api/auth/ws-ticket directly,
// exactly like /terminal/ws-ticket and /events/ws-ticket already require.
func TestVideocallWSTicket_AuthenticatedUser_IssuesTicket(t *testing.T) {
	_, mux := newVideocallTestAPI(&fakeVideocallRoomLister{})

	req := httptest.NewRequest(http.MethodPost, "/api/mobile/v1/videocall/ws-ticket", nil)
	req = req.WithContext(auth.WithUser(req.Context(), "sam"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var body WSTicketResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
	}
	if body.Ticket == "" {
		t.Errorf("empty ticket, expected a one-shot ticket")
	}
	if body.ExpiresIn != int(auth.WSTicketTTL.Seconds()) {
		t.Errorf("expires_in = %d, want %d", body.ExpiresIn, int(auth.WSTicketTTL.Seconds()))
	}
}

func TestVideocallWSTicket_Unauthenticated401(t *testing.T) {
	_, mux := newVideocallTestAPI(&fakeVideocallRoomLister{})

	req := httptest.NewRequest(http.MethodPost, "/api/mobile/v1/videocall/ws-ticket", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body=%s)", rec.Code, rec.Body.String())
	}
}
