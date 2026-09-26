package mobilebff

// handlers_terminal_sessions.go closes the distance between the web panel's
// sessions page and the app's: kill a session, decide who it shows up for and
// peek at what is running in it without attaching.
//
// WHAT IS NOT HERE, AND WHY. The web panel has a fourth button, "Desativar"
// (detach). It does not make the cut because it does nothing: in the `dtach`
// backend — the only one in use — `Detach` is `func (dtachBackend)
// Detach(string) error { return nil }`
// (`internal/pty/backend_dtach.go:381`). dtach keeps no client list to evict
// from; whoever leaves is the process that closes the socket. The button
// exists on the web by inheritance from an older engine, and repeating it
// here would mean handing over an inert control next to one that KILLS the
// session — the worst possible neighbourhood for a button that does not
// respond.
//
// No new ownership rule is born here: it is the same `ptysvc.OwnsSession` as
// the panel, and the 404-instead-of-403 is deliberate — a 403 would confirm
// that the name exists in another account.

import (
	"context"
	"net/http"
	"sort"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/config"
	"server-control-panel/internal/httpx"
	ptysvc "server-control-panel/internal/pty"
)

func init() { Register("terminal-sessions", registerTerminalSessions) }

// KillSessionRequest — the name of the session to terminate.
type KillSessionRequest struct {
	Name string `json:"name" doc:"Nome da sessão a encerrar" required:"true"`
}

type killSessionInput struct {
	Body KillSessionRequest
}

// AssignSessionRequest — who the session starts showing up for.
//
// [Target] accepts a user or `*` for "everyone". It is the same vocabulary as the
// web panel's; translating it into an enum here would create two languages for the
// same field, and the translation would be wrong the day the panel accepted a new
// value.
type AssignSessionRequest struct {
	Name   string `json:"name" doc:"Nome da sessão" required:"true"`
	Target string `json:"target" doc:"Usuário dono, ou '*' para todos" required:"true"`
}

type assignSessionInput struct {
	// Assigning is already idempotent by nature (it forces the owner; repeating
	// gives the same result), but the key rides along because the action travels
	// in the SAME app queue as rename and delete — and a queue where only some
	// items carry a key is a queue with two rules.
	IdemKey string `header:"Idempotency-Key" doc:"Chave gerada pelo aparelho; repetir a mesma chave devolve o resultado da primeira vez, sem executar de novo"`
	Body    AssignSessionRequest
}

type sessionPreviewInput struct {
	Name string `query:"name" doc:"Nome da sessão" required:"true"`
	// The cap exists because the preview is a card, not a terminal: more than a
	// few dozen lines does not fit on screen and does not help decide whether
	// this is the session being looked for — which is the only question the
	// preview answers.
	Lines int `query:"lines" doc:"Quantas linhas do fim (1..200)" default:"20"`
}

// SessionPreviewResponse — the last lines, already stripped of escapes.
//
// Plain text and not raw bytes, unlike `/terminal/log-bruto`: that one feeds a
// terminal emulator and needs the escapes; this one feeds a preview card, where an
// escape is just garbage on screen.
type SessionPreviewResponse struct {
	Name  string `json:"name"`
	Text  string `json:"text"`
	Lines int    `json:"lines"`
}

// AssignTargetsResponse: the possible targets of a reassignment.
type AssignTargetsResponse struct {
	Targets []string `json:"targets" doc:"Nomes de usuário, mais \"*\" para todos"`
}

type assignTargetsOutput struct {
	Body AssignTargetsResponse
}

type sessionPreviewOutput struct {
	Body SessionPreviewResponse
}

func registerTerminalSessions(api huma.API, deps Deps) {
	cfg := deps.Cfg
	own := deps.SessionOwn
	audit := deps.Audit

	huma.Register(api, huma.Operation{
		OperationID: "killTerminalSession",
		Method:      http.MethodPost,
		Path:        "/terminal/sessions/kill",
		Summary:     "Encerra uma sessão e os processos que rodam nela",
		Tags:        []string{"mobile", "terminal"},
		Middlewares: huma.Middlewares{comAuditoria},
		Errors:      []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusBadRequest},
	}, killSessionHandler(cfg, own, audit))

	huma.Register(api, huma.Operation{
		OperationID: "assignTerminalSession",
		Method:      http.MethodPost,
		Path:        "/terminal/sessions/assign",
		Summary:     "Define para quem a sessão aparece",
		Tags:        []string{"mobile", "terminal"},
		Middlewares: huma.Middlewares{comAuditoria},
		Errors:      []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusBadRequest},
	}, assignSessionHandler(cfg, own, audit, deps.Idem))

	huma.Register(api, huma.Operation{
		OperationID: "previewTerminalSession",
		Method:      http.MethodGet,
		Path:        "/terminal/sessions/preview",
		Summary:     "As últimas linhas de uma sessão, sem anexar nela",
		Tags:        []string{"mobile", "terminal"},
		Errors:      []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusBadRequest},
	}, previewSessionHandler(cfg, own))

	huma.Register(api, huma.Operation{
		OperationID: "listAssignTargets",
		Method:      http.MethodGet,
		Path:        "/terminal/sessions/assign-targets",
		Summary:     "Para quem uma sessão PODE ser atribuída",
		Description: "Só admin. Existe para o aplicativo poder OFERECER a lista " +
			"em vez de pedir que se digite um nome de usuário — digitar aqui " +
			"erra em silêncio: um alvo inexistente é aceito e a sessão some da " +
			"lista de todo mundo.",
		Tags:   []string{"mobile", "terminal"},
		Errors: []int{http.StatusUnauthorized, http.StatusNotFound},
	}, assignTargetsHandler(cfg))
}

func assignTargetsHandler(
	cfg *config.Config,
) func(context.Context, *struct{}) (*assignTargetsOutput, error) {
	return func(ctx context.Context, _ *struct{}) (*assignTargetsOutput, error) {
		user := auth.UserFromContext(ctx)
		// Same gate as assign, and for the same reason: whoever cannot reassign
		// cannot even enumerate the server's accounts.
		if !httpx.IsAdmin(cfg, user) {
			return nil, huma.Error404NotFound("not found")
		}
		targets := make([]string, 0, len(cfg.Users)+1)
		for _, u := range cfg.Users {
			targets = append(targets, u.Username)
		}
		sort.Strings(targets)
		// "*" last and on its own: it is the target that changes the outcome most (the
		// session starts showing up for EVERYONE) and it is not a user.
		targets = append(targets, ptysvc.AudienceAll)

		out := &assignTargetsOutput{}
		out.Body.Targets = targets
		return out, nil
	}
}

func killSessionHandler(
	cfg *config.Config,
	own *ptysvc.Ownership,
	audit *auth.AuditLog,
) func(context.Context, *killSessionInput) (*statusOutput, error) {
	return func(ctx context.Context, in *killSessionInput) (*statusOutput, error) {
		user := auth.UserFromContext(ctx)
		nome := strings.TrimSpace(in.Body.Name)
		if nome == "" {
			return nil, huma.Error400BadRequest("name is required")
		}
		if !ptysvc.OwnsSession(user, nome, httpx.IsAdmin(cfg, user), own) {
			return nil, huma.Error404NotFound("session not found")
		}
		if err := ptysvc.SessionKill(nome); err != nil {
			return nil, huma.Error500InternalServerError(err.Error())
		}
		auditar(ctx, audit, user, "terminal.kill", nome)

		out := &statusOutput{}
		out.Body.Status = "ok"
		return out, nil
	}
}

func assignSessionHandler(
	cfg *config.Config,
	own *ptysvc.Ownership,
	audit *auth.AuditLog,
	idem *Idempotencia,
) func(context.Context, *assignSessionInput) (*statusOutput, error) {
	return func(ctx context.Context, in *assignSessionInput) (*statusOutput, error) {
		return rememberResult(idem, keyFromContext(ctx, in.IdemKey), func() (*statusOutput, error) {
			user := auth.UserFromContext(ctx)
			nome := strings.TrimSpace(in.Body.Name)
			target := strings.TrimSpace(in.Body.Target)
			if nome == "" || target == "" {
				return nil, huma.Error400BadRequest("name and target are required")
			}
			// ONLY THE ADMIN REASSIGNS. Reassigning means giving ANOTHER account access
			// to a live shell — that is privilege escalation, not list tidying.
			// A non-admin does not even find out the session exists.
			if !httpx.IsAdmin(cfg, user) {
				return nil, huma.Error404NotFound("session not found")
			}
			if !ptysvc.OwnsSession(user, nome, true, own) {
				return nil, huma.Error404NotFound("session not found")
			}
			if err := own.Assign(ptysvc.SafeSessionName(nome), target); err != nil {
				return nil, huma.Error500InternalServerError(err.Error())
			}
			auditar(ctx, audit, user, "terminal.assign", nome+"→"+target)

			out := &statusOutput{}
			out.Body.Status = "ok"
			return out, nil
		})
	}
}

func previewSessionHandler(
	cfg *config.Config,
	own *ptysvc.Ownership,
) func(context.Context, *sessionPreviewInput) (*sessionPreviewOutput, error) {
	return func(ctx context.Context, in *sessionPreviewInput) (*sessionPreviewOutput, error) {
		user := auth.UserFromContext(ctx)
		nome := strings.TrimSpace(in.Name)
		if nome == "" {
			return nil, huma.Error400BadRequest("name is required")
		}
		if !ptysvc.OwnsSession(user, nome, httpx.IsAdmin(cfg, user), own) {
			return nil, huma.Error404NotFound("session not found")
		}
		lines := in.Lines
		// Clamped on both sides: 0 or negative would return empty and look like a
		// session with no output; above the cap it becomes a log transfer disguised
		// as a preview — and `/terminal/log-bruto` exists for that.
		if lines <= 0 {
			lines = 20
		}
		if lines > 200 {
			lines = 200
		}
		text := ptysvc.SessionTail(nome, lines)

		out := &sessionPreviewOutput{}
		out.Body = SessionPreviewResponse{
			Name:  nome,
			Text:  text,
			Lines: lines,
		}
		return out, nil
	}
}
