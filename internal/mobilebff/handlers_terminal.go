package mobilebff

// handlers_terminal.go registers the three routes the app uses for the
// terminal in your pocket: list sessions, get a one-shot WS ticket and read
// the scrollback of an existing session. The app is forbidden from calling
// /api/auth/ws-ticket or /api/terminal/* directly — only /ws/shell itself
// escapes that, being a WebSocket URL. Every handler here is a thin wrapper
// over internal/pty/internal/auth: no new ownership/quota rule is born in
// this file — if something here looks like new domain logic, that is a sign
// of scope creep, not of real need.
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

// terminalJTIKey holds the jti (the JWT's session claim) that the captureJTI
// middleware reads off the raw *http.Request (auth.JTIFrom) and passes on to
// the typed huma handler — which only receives a context.Context, never the raw
// request. A key private to this file; it is not the same mechanism as
// internal/auth (that one sets it on the *http.Request before huma enters the
// picture; this one only forwards the already-resolved value inside the
// huma.Context).
type terminalJTIKey struct{}

// captureJTI is requireAuth (handlers_session.go) plus capturing the session's
// jti — needed only to mint the WS ticket, which binds it to (user, jti)
// exactly as handleWSTicket already does for the web panel
// (internal/api/handlers_auth.go).
func captureJTI(ctx huma.Context, next func(huma.Context)) {
	req, w := humago.Unwrap(ctx)
	if auth.UserFrom(req) == "" {
		httpx.WriteErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	next(huma.WithValue(ctx, terminalJTIKey{}, auth.JTIFrom(req)))
}

// TerminalSessionSummary projects the map[string]any that
// ptysvc.SessionListForUser already returns (backend_dtach.go List()) — only
// the fields that return value actually has, none invented.
type TerminalSessionSummary struct {
	Name     string `json:"name"`
	Tab      string `json:"tab,omitempty"`
	Created  int64  `json:"created"`
	Attached bool   `json:"attached"`
}

type terminalSessionsOutput struct {
	Body []TerminalSessionSummary
}

// WSTicketRequest is the body of POST .../terminal/ws-ticket. Name is the
// session the app wants to connect to — it may not exist yet: /ws/shell creates
// it on the first connection (same semantics as the web panel).
type WSTicketRequest struct {
	Name string `json:"name"`
}

type wsTicketInput struct {
	Body WSTicketRequest
}

// WSTicketResponse mirrors exactly the body handleWSTicket already returns for
// the web panel — the same mechanism, re-exposed, not a new one.
type WSTicketResponse struct {
	Ticket    string `json:"ticket"`
	ExpiresIn int    `json:"expires_in"`
}

type wsTicketOutput struct {
	Body WSTicketResponse
}

// ScrollbackResponse mirrors the body of handleTerminalScrollback
// (internal/api/handlers_users.go).
type ScrollbackResponse struct {
	Data string `json:"data"`
}

type scrollbackInput struct {
	Name  string `query:"name" required:"true" doc:"Nome da sessão de terminal"`
	Lines int    `query:"lines" default:"5000" doc:"Quantidade máxima de linhas de histórico"`
	Plain bool   `query:"plain" doc:"Se true, devolve texto sem escapes ANSI (para copiar)"`
}

type scrollbackOutput struct {
	// NEVER CACHE. See [semCache].
	CacheControl string `header:"Cache-Control"`
	Body         ScrollbackResponse
}

// semCache is the `Cache-Control` value of the routes whose body is a SLICE OF
// A LIVE STREAM — the terminal session's log.
//
// The same URL returns different content every second, so storing it is
// semantically wrong: there is no such thing as "the response of this URL". And
// the consequence is not stale data on screen, it is a CORRUPTED screen — the
// app replays that log into its own emulator to rebuild the session, and an old
// slice applied before the live stream paints one picture over another.
//
// That is exactly what happened: the app's read cache (`CacheDeLeitura`) makes
// every successful GET cacheable and, when the network fails — which Android 15+
// causes all by itself by cutting the app's network in the background — it
// serves a copy up to seven days old. The owner only escaped by clearing the
// app's storage.
//
// `no-store` is what the app's cache already respects explicitly, so saying it
// here fixes any version of it.
const semCache = "no-store"

// RawLogResponse delivers the session's RAW log — the bytes the PTY wrote,
// escapes and all — for the app to replay in its own emulator.
//
// ## Why base64, and not the string directly
//
// The log is not text: it is a terminal stream, with control bytes and, after a
// rotation in the middle of a character, truncated UTF-8 sequences. A JSON
// string field would run those bytes through Go's escaper (every ESC becomes
// "\u001b", six characters for one) and would replace every invalid byte with
// U+FFFD — corrupting precisely the escape sequences that give the stream its
// meaning. base64 carries any byte through without interpreting any of them,
// and the transport's gzip recovers most of the 33% bloat.
//
// ## Total answers one question only
//
// "Did I get the whole log?" — if Total > Bytes, there is older history that did
// not fit in the request. The app uses that to tell the truth on screen instead
// of suggesting that is all there ever was.
type RawLogResponse struct {
	Base64 string `json:"base64" doc:"Log cru da sessão, codificado em base64"`
	Bytes  int    `json:"bytes" doc:"Quantos bytes de log estão nesta resposta"`
	Total  int    `json:"total" doc:"Tamanho total do log disponível no servidor"`
}

// HistoricoResponse delivers the session's RENDERED history — the lines that
// have already scrolled off the screen, as append-only text.
//
// The difference from the raw log is not one of format, it is one of nature:
//
//	raw log = everything that went over the wire, including the drawing in progress
//	history = what the person SAW, once each
//
// Replaying the raw log into a fresh grid duplicates: the `ESC[nA` of a program
// that repaints saturates at the top of the SCREEN and never reaches the
// scrollback, so the earlier copy stays. It is the defect that `ReplayDeAttach`'s
// KDoc describes as unfixable — and it is: there is no fix on the READING side.
// The server fixes it on the WRITING side, by keeping a live emulator on the
// session's grid.
//
// Measured on a real production log: 4096 KiB of raw log yield 360 KiB of
// history — 11.4x more conversation per byte on the same network budget.
//
// base64 for the same reason as the raw log: there are still SGR sequences in
// the stream.
type HistoricoResponse struct {
	Base64 string `json:"base64" doc:"Histórico renderizado da sessão, codificado em base64"`
	Bytes  int    `json:"bytes" doc:"Quantos bytes de histórico estão nesta resposta"`
	Total  int    `json:"total" doc:"Tamanho total do histórico disponível no servidor"`
}

type historicoInput struct {
	Name  string `query:"name" required:"true" doc:"Nome da sessão de terminal"`
	Bytes int    `query:"bytes" default:"2097152" doc:"Teto de bytes do recorte final do histórico"`
}

type historicoOutput struct {
	// NEVER CACHE, for the same reason as the raw log.
	CacheControl string `header:"Cache-Control"`
	Body         HistoricoResponse
}

type rawLogInput struct {
	Name  string `query:"name" required:"true" doc:"Nome da sessão de terminal"`
	Bytes int    `query:"bytes" default:"4194304" doc:"Teto de bytes do recorte final do log"`
}

type rawLogOutput struct {
	// NEVER CACHE. See [semCache] — this is the route the app replays into
	// libghostty-vt itself, and it is where an old slice scrambles the screen
	// instead of merely ageing it.
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
		Summary:     "Sessões de terminal visíveis ao usuário autenticado",
		Tags:        []string{"mobile", "terminal"},
		Middlewares: huma.Middlewares{requireAuth},
		Errors:      []int{http.StatusUnauthorized},
	}, terminalSessionsHandler(cfg, own))

	huma.Register(api, huma.Operation{
		OperationID: "issueTerminalWSTicket",
		Method:      http.MethodPost,
		Path:        "/terminal/ws-ticket",
		Summary:     "Emite um ticket WS one-shot (60s) para anexar em /ws/shell",
		Tags:        []string{"mobile", "terminal"},
		Middlewares: huma.Middlewares{captureJTI},
		Errors:      []int{http.StatusUnauthorized, http.StatusNotFound},
	}, terminalWSTicketHandler(cfg, own))

	huma.Register(api, huma.Operation{
		OperationID: "getTerminalScrollback",
		Method:      http.MethodGet,
		Path:        "/terminal/scrollback",
		Summary:     "Histórico (tail) de uma sessão de terminal que o usuário possui",
		Tags:        []string{"mobile", "terminal"},
		Middlewares: huma.Middlewares{requireAuth},
		Errors:      []int{http.StatusUnauthorized, http.StatusNotFound},
	}, terminalScrollbackHandler(cfg, own))

	huma.Register(api, huma.Operation{
		OperationID: "getTerminalRawLog",
		Method:      http.MethodGet,
		Path:        "/terminal/log-bruto",
		Summary:     "Log cru (bytes do PTY) de uma sessão, para o app primar o próprio emulador",
		Tags:        []string{"mobile", "terminal"},
		Middlewares: huma.Middlewares{requireAuth},
		Errors:      []int{http.StatusUnauthorized, http.StatusNotFound},
	}, terminalRawLogHandler(cfg, own))

	huma.Register(api, huma.Operation{
		OperationID: "getTerminalHistorico",
		Method:      http.MethodGet,
		Path:        "/terminal/historico",
		Summary:     "Histórico renderizado de uma sessão — o que a pessoa viu, uma vez cada",
		Tags:        []string{"mobile", "terminal"},
		Middlewares: huma.Middlewares{requireAuth},
		Errors:      []int{http.StatusUnauthorized, http.StatusNotFound},
	}, terminalHistoricoHandler(cfg, own))
}

func terminalSessionsHandler(cfg *config.Config, own *ptysvc.Ownership) func(ctx context.Context, in *struct{}) (*terminalSessionsOutput, error) {
	return func(ctx context.Context, _ *struct{}) (*terminalSessionsOutput, error) {
		user := auth.UserFromContext(ctx)
		primary := httpx.IsAdmin(cfg, user)
		sessions, err := ptysvc.SessionListForUser(user, primary, own)
		if err != nil {
			// Graceful: no live session is not an error yet, it is an empty list — same
			// behaviour as handleTerminalSessions.
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
			// Mirrors the ownership gate that /ws/shell (HostShell,
			// internal/pty/pty.go) already applies on attach — not a new rule.
			// owner == "" (a name never claimed) always passes: /ws/shell creates the
			// session on the first connection and whoever connects becomes the owner (no
			// privilege gained). owner == user or owner == AudienceAll ("Todos")
			// also pass — that is attaching to something already yours or
			// shared. Only owner != "" && owner != user (a name already
			// claimed by ANOTHER specific user) denies, and only when the
			// caller is not an admin (the sessions admin/master may attach to
			// any session, the same exception /ws/shell itself makes) — 404, never
			// 403, so as not to leak existence (same rule as
			// handleTerminalScrollback).
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
		return &scrollbackOutput{CacheControl: semCache, Body: ScrollbackResponse{Data: data}}, nil
	}
}

func terminalRawLogHandler(cfg *config.Config, own *ptysvc.Ownership) func(ctx context.Context, in *rawLogInput) (*rawLogOutput, error) {
	return func(ctx context.Context, in *rawLogInput) (*rawLogOutput, error) {
		user := auth.UserFromContext(ctx)
		name := strings.TrimSpace(in.Name)
		// Same ownership gate as the scrollback: 404, never 403 — the existence of
		// somebody else's session does not leak. Here it weighs more than in the
		// listing: the raw log is the literal transcript of everything that went
		// through the terminal.
		if !ptysvc.OwnsSession(user, name, httpx.IsAdmin(cfg, user), own) {
			return nil, huma.Error404NotFound("session not found")
		}
		data, total := ptysvc.SessionRawLogTail(user, name, in.Bytes)
		return &rawLogOutput{CacheControl: semCache, Body: RawLogResponse{
			Base64: base64.StdEncoding.EncodeToString(data),
			Bytes:  len(data),
			Total:  total,
		}}, nil
	}
}

func terminalHistoricoHandler(cfg *config.Config, own *ptysvc.Ownership) func(ctx context.Context, in *historicoInput) (*historicoOutput, error) {
	return func(ctx context.Context, in *historicoInput) (*historicoOutput, error) {
		user := auth.UserFromContext(ctx)
		name := strings.TrimSpace(in.Name)
		// Same ownership gate as the raw log: 404, never 403.
		if !ptysvc.OwnsSession(user, name, httpx.IsAdmin(cfg, user), own) {
			return nil, huma.Error404NotFound("session not found")
		}
		data, total := ptysvc.HistoricoDaSessao(user, name, in.Bytes)
		return &historicoOutput{CacheControl: semCache, Body: HistoricoResponse{
			Base64: base64.StdEncoding.EncodeToString(data),
			Bytes:  len(data),
			Total:  total,
		}}, nil
	}
}

// stringField/int64Field/boolField safely extract a typed value from a
// map[string]any — the shape ptysvc.SessionListForUser returns (never a Go
// struct) — without invoking reflection or assuming the key is present.
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
