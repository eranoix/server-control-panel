package mobilebff

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/videocall"
)

// registerVideocall registers the room listing for the authenticated user's
// video calls — the foundation the native Android client consumes to know which
// rooms it may join, without duplicating any room logic: the endpoint only
// calls Service.ListForUser, the same query /api/videocall/rooms (web panel)
// already uses.
func init() { Register("videocall", registerVideocall) }

// videocallRoomLister is the minimal slice of *videocall.Service that this
// file calls — an interface, not the concrete type, so that tests can
// substitute a fake without having to open a real *videocall.Service
// (which spins up flush/heartbeat goroutines and writes to disk).
// *videocall.Service satisfies this through the method already exported in
// service_rooms.go.
type videocallRoomLister interface {
	ListForUser(user string) []videocall.Room
}

func registerVideocall(api huma.API, deps Deps) {
	// Explicit conversion BEFORE it becomes an interface: assigning a nil
	// *videocall.Service straight to a videocallRoomLister variable would
	// produce a NON-nil interface carrying a nil pointer (Go's classic
	// interface gotcha) — svc == nil in the handler would fail and
	// ListForUser would be called with a nil receiver, panicking. Checking
	// here, outside the interface, is what guarantees that Deps.Videocall == nil
	// becomes a genuinely nil interface.
	var svc videocallRoomLister
	if deps.Videocall != nil {
		svc = deps.Videocall
	}
	registerVideocallWithService(api, svc)
}

// registerVideocallWithService exists separately from registerVideocall so that
// tests can inject a fake videocallRoomLister and still exercise the real huma
// route registration — the same code tree that runs in production. svc may be
// nil (typed nil included): the route then answers an empty list, the same
// degradation pattern as the other Deps fields.
func registerVideocallWithService(api huma.API, svc videocallRoomLister) {
	huma.Register(api, huma.Operation{
		OperationID: "listVideocallRooms",
		Method:      http.MethodGet,
		Path:        "/videocall/rooms",
		Summary:     "Lista as salas de videochamada do usuário autenticado (dono ou membro)",
		Tags:        []string{"mobile", "videocall"},
		Middlewares: huma.Middlewares{requireAuth},
		Errors:      []int{http.StatusUnauthorized},
	}, listVideocallRoomsHandler(svc))

	huma.Register(api, huma.Operation{
		OperationID: "issueVideocallWSTicket",
		Method:      http.MethodPost,
		Path:        "/videocall/ws-ticket",
		Summary:     "Emite um ticket WS one-shot (60s) para anexar em /ws/videocall",
		Tags:        []string{"mobile", "videocall"},
		Middlewares: huma.Middlewares{captureJTI},
		Errors:      []int{http.StatusUnauthorized},
	}, videocallWSTicketHandler())
}

type videocallRoomsOutput struct {
	Body []videocall.Room
}

// videocallWSTicketOutput mirrors WSTicketResponse (handlers_terminal.go) —
// same one-shot-ticket mechanism reused for a third WS endpoint, not a new
// one. The app is forbidden from calling /api/auth/ws-ticket directly
// (that route is the cookie-authenticated web panel's own surface); every
// mobile WS endpoint mints its ticket through a thin BFF wrapper like this
// one instead, exactly as /terminal/ws-ticket and /events/ws-ticket already
// do. No per-room ownership check here (unlike terminal's ws-ticket, which
// checks session ownership before minting): /ws/videocall's HandleWS already
// re-validates room membership via Service.CanJoin at connect time, using
// the room_id query parameter — minting the ticket earlier, before the room
// is even known to this handler, cannot duplicate or weaken that check.
func videocallWSTicketHandler() func(ctx context.Context, in *struct{}) (*videocallWSTicketOutput, error) {
	return func(ctx context.Context, _ *struct{}) (*videocallWSTicketOutput, error) {
		user := auth.UserFromContext(ctx)
		jti, _ := ctx.Value(terminalJTIKey{}).(string)
		ticket := auth.IssueWSTicket(user, jti)
		return &videocallWSTicketOutput{Body: WSTicketResponse{
			Ticket:    ticket,
			ExpiresIn: int(auth.WSTicketTTL.Seconds()),
		}}, nil
	}
}

type videocallWSTicketOutput struct {
	Body WSTicketResponse
}

func listVideocallRoomsHandler(svc videocallRoomLister) func(ctx context.Context, input *struct{}) (*videocallRoomsOutput, error) {
	return func(ctx context.Context, _ *struct{}) (*videocallRoomsOutput, error) {
		user := auth.UserFromContext(ctx)
		if user == "" {
			return nil, huma.Error401Unauthorized("unauthorized")
		}
		if svc == nil {
			return &videocallRoomsOutput{Body: []videocall.Room{}}, nil
		}
		rooms := svc.ListForUser(user)
		if rooms == nil {
			rooms = []videocall.Room{}
		}
		return &videocallRoomsOutput{Body: rooms}, nil
	}
}
