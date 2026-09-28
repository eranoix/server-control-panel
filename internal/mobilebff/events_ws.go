package mobilebff

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/gorilla/websocket"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/wsorigin"
)

func init() { Register("events", registerEvents) }

func registerEvents(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "issueMobileEventsWSTicket",
		Method:      http.MethodPost,
		Path:        "/events/ws-ticket",
		Summary:     "Issues a one-shot WS ticket (60s) to attach to /ws/mobile-events",
		Tags:        []string{"mobile", "events"},
		Middlewares: huma.Middlewares{captureJTI},
		Errors:      []int{http.StatusUnauthorized},
	}, eventsWSTicketHandler())
}

type eventsWSTicketOutput struct {
	Body WSTicketResponse
}

func eventsWSTicketHandler() func(ctx context.Context, in *struct{}) (*eventsWSTicketOutput, error) {
	return func(ctx context.Context, _ *struct{}) (*eventsWSTicketOutput, error) {
		user := auth.UserFromContext(ctx)
		jti, _ := ctx.Value(terminalJTIKey{}).(string)
		ticket := auth.IssueWSTicket(user, jti)
		return &eventsWSTicketOutput{Body: WSTicketResponse{
			Ticket:    ticket,
			ExpiresIn: int(auth.WSTicketTTL.Seconds()),
		}}, nil
	}
}

const (
	eventsWSPingPeriod = 25 * time.Second
	eventsWSPongWait   = 45 * time.Second
	eventsWSWriteWait  = 10 * time.Second
	eventsWSReadLimit  = 4096
)

var eventsUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     wsorigin.CheckSameHost,
}

type controlFrame struct {
	Op      string `json:"op"`
	Channel string `json:"channel"`
}

type ackFrame struct {
	Op      string `json:"op"`
	Channel string `json:"channel"`
}

func HandleMobileEventsWS(authSvc *auth.Service, hub *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		user, _ := authSvc.ExtractWSAuth(req)
		if user == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		conn, err := eventsUpgrader.Upgrade(w, req, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		var writeMu sync.Mutex
		writeJSON := func(v any) error {
			writeMu.Lock()
			defer writeMu.Unlock()
			_ = conn.SetWriteDeadline(time.Now().Add(eventsWSWriteWait))
			return conn.WriteJSON(v)
		}

		hc := hub.register(user, func(env Envelope) error { return writeJSON(env) })
		defer hub.unregister(hc)

		conn.SetReadLimit(eventsWSReadLimit)
		_ = conn.SetReadDeadline(time.Now().Add(eventsWSPongWait))
		conn.SetPongHandler(func(string) error {
			_ = conn.SetReadDeadline(time.Now().Add(eventsWSPongWait))
			return nil
		})

		done := make(chan struct{})
		go func() {
			defer close(done)
			for {
				_, data, err := conn.ReadMessage()
				if err != nil {
					return
				}
				var frame controlFrame
				if json.Unmarshal(data, &frame) != nil {
					continue
				}
				switch frame.Op {
				case "subscribe":
					if frame.Channel == "" {
						continue
					}
					hc.subscribe(frame.Channel)
					_ = writeJSON(ackFrame{Op: "subscribed", Channel: frame.Channel})
				case "unsubscribe":
					if frame.Channel == "" {
						continue
					}
					hc.unsubscribe(frame.Channel)
					_ = writeJSON(ackFrame{Op: "unsubscribed", Channel: frame.Channel})
				default:
				}
			}
		}()

		ping := time.NewTicker(eventsWSPingPeriod)
		defer ping.Stop()
		for {
			select {
			case <-done:
				return
			case <-req.Context().Done():
				return
			case <-ping.C:
				writeMu.Lock()
				_ = conn.SetWriteDeadline(time.Now().Add(eventsWSWriteWait))
				err := conn.WriteMessage(websocket.PingMessage, nil)
				writeMu.Unlock()
				if err != nil {
					return
				}
			}
		}
	}
}
