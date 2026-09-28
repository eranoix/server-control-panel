package mobilebff

import (
	"context"
	"encoding/base64"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/config"
	"server-control-panel/internal/httpx"
	ptysvc "server-control-panel/internal/pty"
)

func init() { Register("terminal", registerTerminal) }

type terminalJTIKey struct{}

func captureJTI(ctx huma.Context, next func(huma.Context)) {
	req, w := humago.Unwrap(ctx)
	if auth.UserFrom(req) == "" {
		httpx.WriteErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	next(huma.WithValue(ctx, terminalJTIKey{}, auth.JTIFrom(req)))
}

type TerminalSessionSummary struct {
	Name     string `json:"name"`
	Tab      string `json:"tab,omitempty"`
	Created  int64  `json:"created"`
	Attached bool   `json:"attached"`
}

type terminalSessionsOutput struct {
	Body []TerminalSessionSummary
}

type WSTicketRequest struct {
	Name string `json:"name"`
}

type wsTicketInput struct {
	Body WSTicketRequest
}

type WSTicketResponse struct {
	Ticket    string `json:"ticket"`
	ExpiresIn int    `json:"expires_in"`
}

type wsTicketOutput struct {
	Body WSTicketResponse
}

type ScrollbackResponse struct {
	Data string `json:"data"`
}

type scrollbackInput struct {
	Name  string `query:"name" required:"true" doc:"Terminal session name"`
	Lines int    `query:"lines" default:"5000" doc:"Maximum number of history lines"`
	Plain bool   `query:"plain" doc:"If true, returns text without ANSI escapes (for copying)"`
}

type scrollbackOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         ScrollbackResponse
}

const noCache = "no-store"

type RawLogResponse struct {
	Base64 string `json:"base64" doc:"Raw session log, base64-encoded"`
	Bytes  int    `json:"bytes" doc:"How many log bytes are in this response"`
	Total  int    `json:"total" doc:"Total size of the log available on the server"`
}

type HistoryResponse struct {
	Base64 string `json:"base64" doc:"Rendered session history, base64-encoded"`
	Bytes  int    `json:"bytes" doc:"How many history bytes are in this response"`
	Total  int    `json:"total" doc:"Total size of the history available on the server"`
}

type historyInput struct {
	Name  string `query:"name" required:"true" doc:"Terminal session name"`
	Bytes int    `query:"bytes" default:"2097152" doc:"Byte cap for the final slice of the history"`
}

type historyOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         HistoryResponse
}

type rawLogInput struct {
	Name  string `query:"name" required:"true" doc:"Terminal session name"`
	Bytes int    `query:"bytes" default:"4194304" doc:"Byte cap for the final slice of the log"`
}

type rawLogOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         RawLogResponse
}

func registerTerminal(api huma.API, deps Deps) {
	cfg := deps.Cfg
	own := deps.SessionOwn

	huma.Register(api, huma.Operation{
		OperationID: "listTerminalSessions",
		Method:      http.MethodGet,
		Path:        "/terminal/sessions",
		Summary:     "Terminal sessions visible to the authenticated user",
		Tags:        []string{"mobile", "terminal"},
		Middlewares: huma.Middlewares{requireAuth},
		Errors:      []int{http.StatusUnauthorized},
	}, terminalSessionsHandler(cfg, own))

	huma.Register(api, huma.Operation{
		OperationID: "issueTerminalWSTicket",
		Method:      http.MethodPost,
		Path:        "/terminal/ws-ticket",
		Summary:     "Issues a one-shot WS ticket (60s) to attach to /ws/shell",
		Tags:        []string{"mobile", "terminal"},
		Middlewares: huma.Middlewares{captureJTI},
		Errors:      []int{http.StatusUnauthorized, http.StatusNotFound},
	}, terminalWSTicketHandler(cfg, own))

	huma.Register(api, huma.Operation{
		OperationID: "getTerminalScrollback",
		Method:      http.MethodGet,
		Path:        "/terminal/scrollback",
		Summary:     "History (tail) of a terminal session the user owns",
		Tags:        []string{"mobile", "terminal"},
		Middlewares: huma.Middlewares{requireAuth},
		Errors:      []int{http.StatusUnauthorized, http.StatusNotFound},
	}, terminalScrollbackHandler(cfg, own))

	huma.Register(api, huma.Operation{
		OperationID: "getTerminalRawLog",
		Method:      http.MethodGet,
		Path:        "/terminal/raw-log",
		Summary:     "Raw log (PTY bytes) of a session, so the app can prime its own emulator",
		Tags:        []string{"mobile", "terminal"},
		Middlewares: huma.Middlewares{requireAuth},
		Errors:      []int{http.StatusUnauthorized, http.StatusNotFound},
	}, terminalRawLogHandler(cfg, own))

	huma.Register(api, huma.Operation{
		OperationID: "getTerminalHistory",
		Method:      http.MethodGet,
		Path:        "/terminal/history",
		Summary:     "Rendered history of a session: what the person saw, once each",
		Tags:        []string{"mobile", "terminal"},
		Middlewares: huma.Middlewares{requireAuth},
		Errors:      []int{http.StatusUnauthorized, http.StatusNotFound},
	}, terminalHistoryHandler(cfg, own))
}

func terminalSessionsHandler(cfg *config.Config, own *ptysvc.Ownership) func(ctx context.Context, in *struct{}) (*terminalSessionsOutput, error) {
	return func(ctx context.Context, _ *struct{}) (*terminalSessionsOutput, error) {
		user := auth.UserFromContext(ctx)
		primary := httpx.IsAdmin(cfg, user)
		sessions, err := ptysvc.SessionListForUser(user, primary, own)
		if err != nil {
			return &terminalSessionsOutput{Body: []TerminalSessionSummary{}}, nil
		}
		out := make([]TerminalSessionSummary, 0, len(sessions))
		for _, s := range sessions {
			out = append(out, TerminalSessionSummary{
				Name:     stringField(s, "name"),
				Tab:      stringField(s, "tab"),
				Created:  int64Field(s, "created"),
				Attached: boolField(s, "attached"),
			})
		}
		return &terminalSessionsOutput{Body: out}, nil
	}
}

func terminalWSTicketHandler(cfg *config.Config, own *ptysvc.Ownership) func(ctx context.Context, in *wsTicketInput) (*wsTicketOutput, error) {
	return func(ctx context.Context, in *wsTicketInput) (*wsTicketOutput, error) {
		user := auth.UserFromContext(ctx)
		name := strings.TrimSpace(in.Body.Name)
		if name != "" {
			owner := own.Owner(name)
			if owner != "" && owner != user && owner != ptysvc.AudienceAll {
				if !httpx.IsAdmin(cfg, user) {
					return nil, huma.Error404NotFound("session not found")
				}
			}
		}
		jti, _ := ctx.Value(terminalJTIKey{}).(string)
		ticket := auth.IssueWSTicket(user, jti)
		return &wsTicketOutput{Body: WSTicketResponse{
			Ticket:    ticket,
			ExpiresIn: int(auth.WSTicketTTL.Seconds()),
		}}, nil
	}
}

func terminalScrollbackHandler(cfg *config.Config, own *ptysvc.Ownership) func(ctx context.Context, in *scrollbackInput) (*scrollbackOutput, error) {
	return func(ctx context.Context, in *scrollbackInput) (*scrollbackOutput, error) {
		user := auth.UserFromContext(ctx)
		name := strings.TrimSpace(in.Name)
		primary := httpx.IsAdmin(cfg, user)
		if !ptysvc.OwnsSession(user, name, primary, own) {
			return nil, huma.Error404NotFound("session not found")
		}
		lines := in.Lines
		if lines <= 0 {
			lines = 5000
		}
		escapes := !in.Plain
		data := ptysvc.SessionScrollback(user, name, lines, escapes)
		return &scrollbackOutput{CacheControl: noCache, Body: ScrollbackResponse{Data: data}}, nil
	}
}

func terminalRawLogHandler(cfg *config.Config, own *ptysvc.Ownership) func(ctx context.Context, in *rawLogInput) (*rawLogOutput, error) {
	return func(ctx context.Context, in *rawLogInput) (*rawLogOutput, error) {
		user := auth.UserFromContext(ctx)
		name := strings.TrimSpace(in.Name)
		if !ptysvc.OwnsSession(user, name, httpx.IsAdmin(cfg, user), own) {
			return nil, huma.Error404NotFound("session not found")
		}
		data, total := ptysvc.SessionRawLogTail(user, name, in.Bytes)
		return &rawLogOutput{CacheControl: noCache, Body: RawLogResponse{
			Base64: base64.StdEncoding.EncodeToString(data),
			Bytes:  len(data),
			Total:  total,
		}}, nil
	}
}

func terminalHistoryHandler(cfg *config.Config, own *ptysvc.Ownership) func(ctx context.Context, in *historyInput) (*historyOutput, error) {
	return func(ctx context.Context, in *historyInput) (*historyOutput, error) {
		user := auth.UserFromContext(ctx)
		name := strings.TrimSpace(in.Name)
		if !ptysvc.OwnsSession(user, name, httpx.IsAdmin(cfg, user), own) {
			return nil, huma.Error404NotFound("session not found")
		}
		data, total := ptysvc.SessionHistory(user, name, in.Bytes)
		return &historyOutput{CacheControl: noCache, Body: HistoryResponse{
			Base64: base64.StdEncoding.EncodeToString(data),
			Bytes:  len(data),
			Total:  total,
		}}, nil
	}
}

func stringField(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func int64Field(m map[string]any, key string) int64 {
	switch v := m[key].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	default:
		return 0
	}
}

func boolField(m map[string]any, key string) bool {
	v, _ := m[key].(bool)
	return v
}
