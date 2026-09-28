package mobilebff

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/videocall"
)

func init() { Register("videocall", registerVideocall) }

type videocallRoomLister interface {
	ListForUser(user string) []videocall.Room
}

func registerVideocall(api huma.API, deps Deps) {
	var svc videocallRoomLister
	if deps.Videocall != nil {
		svc = deps.Videocall
	}
	registerVideocallWithService(api, svc)
}

func registerVideocallWithService(api huma.API, svc videocallRoomLister) {
	huma.Register(api, huma.Operation{
		OperationID: "listVideocallRooms",
		Method:      http.MethodGet,
		Path:        "/videocall/rooms",
		Summary:     "Lists the authenticated user's video call rooms (owner or member)",
		Tags:        []string{"mobile", "videocall"},
		Middlewares: huma.Middlewares{requireAuth},
		Errors:      []int{http.StatusUnauthorized},
	}, listVideocallRoomsHandler(svc))

	huma.Register(api, huma.Operation{
		OperationID: "issueVideocallWSTicket",
		Method:      http.MethodPost,
		Path:        "/videocall/ws-ticket",
		Summary:     "Issues a one-shot WS ticket (60s) to attach to /ws/videocall",
		Tags:        []string{"mobile", "videocall"},
		Middlewares: huma.Middlewares{captureJTI},
		Errors:      []int{http.StatusUnauthorized},
	}, videocallWSTicketHandler())
}

type videocallRoomsOutput struct {
	Body []videocall.Room
}

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
