package mobilebff

// events_ws.go implements the single multiplexed
// real-time socket every other live surface in this phase (deploy log,
// health dashboard, in-app notification stream) plugs into.
//
//	POST /api/mobile/v1/events/ws-ticket  → mint a one-shot ticket (huma route)
//	GET  /ws/mobile-events                → the socket itself (raw net/http;
//	                                         a WS upgrade cannot be a huma
//	                                         JSON request/response, exactly
//	                                         like /ws/shell, /ws/queue/ and
//	                                         /ws/videocall never are)
//
// Ticket-first auth, not "reuse whatever got you into this request": the
// app already holds a bearer JWT it could attach directly to the WS
// upgrade (net/http WS clients, unlike a browser page, CAN set arbitrary
// headers on that request) — auth.extractToken would accept that Bearer
// header just fine. A one-shot ticket is used anyway for the SAME reason
// Phase 5 (05-01-PLAN.md) minted one for /ws/shell: the long-lived JWT
// never has to leave the app process for this call, and if a ticket ever
// did leak (proxy access log, crash report attaching request state), it is
// worthless after 60s or after the first connect, whichever comes first.
// One idiom, reused by both of this app's WS endpoints, instead of two
// different auth stories for a client that has to implement both.
//
// Because of that choice, /ws/mobile-events cannot sit behind the generic
// auth.Middleware(protected) gate every other protected route uses: that
// middleware 401s before a handler ever runs unless a Bearer/cookie/
// ?token= is already present, which a ticket-only request deliberately
// omits. HandleMobileEventsWS therefore authenticates itself, calling
// auth.Service.ExtractWSAuth directly (ticket first, then the same
// cookie/bearer/?token= fallback every other WS path already tolerates) —
// internal/api.NewRouter registers it on the OUTER mux, before the
// Middleware-wrapped "protected" mux takes over. Every other route this
// package (mobilebff) owns keeps going through Mount/protected/Middleware
// unchanged.
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
		Summary:     "Emite um ticket WS one-shot (60s) para anexar em /ws/mobile-events",
		Tags:        []string{"mobile", "events"},
		Middlewares: huma.Middlewares{captureJTI},
		Errors:      []int{http.StatusUnauthorized},
	}, eventsWSTicketHandler())
}

// eventsWSTicketOutput mirrors WSTicketResponse (handlers_terminal.go) —
// same mechanism re-exposed for a second WS endpoint, not a new shape.
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
	eventsWSReadLimit  = 4096 // control frames only ({"op":...,"channel":...}); generous but bounded
)

var eventsUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     wsorigin.CheckSameHost,
}

// controlFrame is a client->server frame: {"op":"subscribe","channel":"x"}
// or {"op":"unsubscribe","channel":"x"}, per ARCHITECTURE.md.
type controlFrame struct {
	Op      string `json:"op"`
	Channel string `json:"channel"`
}

// ackFrame is the server->client ack for a control frame:
// {"op":"subscribed","channel":"x"} / {"op":"unsubscribed","channel":"x"}.
type ackFrame struct {
	Op      string `json:"op"`
	Channel string `json:"channel"`
}

// HandleMobileEventsWS returns the GET /ws/mobile-events handler. authSvc
// resolves the connecting user (ticket first, then the usual WS fallbacks —
// see ExtractWSAuth); hub is the process-wide connection registry envelopes
// get Published through. Mounted directly on the outer mux by
// internal/api.NewRouter — see this file's header comment for why it is
// not registered on the auth.Middleware-wrapped "protected" mux like every
// other authenticated route.
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

		// One writeMu serializes every writer of this connection: the read
		// loop's own subscribe/unsubscribe acks, the ping ticker below, and
		// any Hub.Publish call landing from an unrelated goroutine (the
		// notify.Router worker, a queue callback, ...). gorilla/websocket
		// forbids concurrent writers on the same *Conn without this.
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

		// done closes when the read loop returns for any reason (client
		// close, abrupt disconnect, protocol error) — the ONLY signal the
		// write side needs to know the connection is gone and clean up via
		// the deferred hub.unregister above.
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
					continue // malformed frame: dropped, not fatal
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
					// unknown op: dropped, not fatal — mirrors the leniency
					// handleQueueWS shows toward control frames.
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
