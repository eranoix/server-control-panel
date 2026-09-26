package mobilebff

// handlers_terminal_backup.go exposes to the app what the web panel already
// did with terminal sessions: back up, list, restore and delete — plus
// rename, which was missing in your pocket.
//
// No new ownership rule is born here: ownership is the same
// `ptysvc.OwnsSession`/`SessionListForUser` as the panel, and persistence is
// the `internal/sessionbackup` the two share. If this file starts deciding
// WHERE a backup lives or HOW it is pruned, that is a sign the extraction
// into the common package has been undone.

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/config"
	"server-control-panel/internal/httpx"
	ptysvc "server-control-panel/internal/pty"
	"server-control-panel/internal/sessionbackup"
)

func init() { Register("terminal-backup", registerTerminalBackup) }

// ipDoClienteKey carries the client IP from the raw `*http.Request` to the typed
// handler, which only receives a context.Context.
//
// It exists because `httpx.AuditEvent` takes the IP from the request, and
// passing `nil` there is not an option: `auth.ClientIP` dereferences
// `r.RemoteAddr` and would panic. Auditing without an IP is no good either —
// "who restored a backup" without "from where" is half an audit record.
type ipDoClienteKey struct{}

// comAuditoria is requireAuth plus capturing the IP. Same shape as
// captureJTI in handlers_terminal.go, for the same reason: the information exists
// in the raw request and the typed handler does not see it.
func comAuditoria(ctx huma.Context, next func(huma.Context)) {
	req, w := humago.Unwrap(ctx)
	if auth.UserFrom(req) == "" {
		httpx.WriteErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	next(huma.WithValue(ctx, ipDoClienteKey{}, auth.ClientIP(req)))
}

// auditar records an event with the IP captured by [comAuditoria].
func auditar(ctx context.Context, log *auth.AuditLog, user, acao, alvo string) {
	if log == nil {
		return
	}
	ip, _ := ctx.Value(ipDoClienteKey{}).(string)
	log.Append(auth.Event{User: user, Action: acao, Target: alvo, IP: ip})
}

// BackupSessionSummary is a session inside a backup, already summarized — the
// whole scrollback never leaves here in a listing.
type BackupSessionSummary struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
	Lines   int    `json:"lines"`
}

// BackupSummary is a backup's metadata.
type BackupSummary struct {
	ID       string                 `json:"id"`
	Created  int64                  `json:"created"`
	Source   string                 `json:"source,omitempty"`
	Bytes    int64                  `json:"bytes"`
	Sessions []BackupSessionSummary `json:"sessions"`
}

type backupsListOutput struct {
	Body []BackupSummary
}

// CreateBackupRequest with an empty Name backs up ALL visible sessions in a
// single bundle — the same default as the web panel.
type CreateBackupRequest struct {
	Name string `json:"name,omitempty" doc:"Sessão a salvar; vazio = todas num pacote"`
}

type createBackupInput struct {
	// IdemKey exists because this is the case that really DUPLICATES: the
	// backup's id is `time.Now().UnixNano()`, so two identical POSTs become two
	// distinct backups on disk. A plain string and not a pointer — huma panics
	// on a pointer in a header parameter, and "" is already a no-op in the
	// table.
	IdemKey string `header:"Idempotency-Key" doc:"Chave gerada pelo aparelho; repetir a mesma chave devolve o resultado da primeira vez, sem executar de novo"`
	Body    CreateBackupRequest
}

// CreateBackupResponse reports how many sessions went in: a backup of 7 sessions
// and a backup of 1 are different things to whoever is going to restore it.
type CreateBackupResponse struct {
	ID      string `json:"id"`
	Created int64  `json:"created"`
	Count   int    `json:"count"`
}

type createBackupOutput struct {
	Body CreateBackupResponse
}

// RestoreBackupRequest with Name restores only that one session from the backup.
type RestoreBackupRequest struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty" doc:"Sessão a restaurar; vazio = todas as do backup"`
}

type restoreBackupInput struct {
	// Restoring twice does not recreate a session (a live name is skipped), but
	// the second response's counters lie: they would say "0 restored, N
	// skipped" for an action that restored N. The key returns the truth of the
	// first time.
	IdemKey string `header:"Idempotency-Key" doc:"Chave gerada pelo aparelho; repetir a mesma chave devolve o resultado da primeira vez, sem executar de novo"`
	Body    RestoreBackupRequest
}

// RestoreBackupResponse separates restored from skipped because the two things
// happen together all the time: an already-live name and a blown quota both
// skip, and a response that only said "ok" would hide that.
type RestoreBackupResponse struct {
	Restored int `json:"restored"`
	Skipped  int `json:"skipped"`
}

type restoreBackupOutput struct {
	Body RestoreBackupResponse
}

type deleteBackupInput struct {
	ID   string `path:"id" doc:"Id do backup"`
	Name string `query:"name" doc:"Remove só esta sessão do backup; vazio = o backup inteiro"`
	// Deleting the ENTIRE backup is already idempotent in the store (`os.Remove`
	// with `os.IsNotExist` tolerated). Deleting ONE session from inside it is not:
	// that path READS the backup before touching it, and with the file already
	// removed it answers 404. To whoever is in the send queue that 404 is
	// indistinguishable from "never existed" — and the queue discards 4xx. The key
	// returns the original 200.
	IdemKey string `header:"Idempotency-Key" doc:"Chave gerada pelo aparelho; repetir a mesma chave devolve o resultado da primeira vez, sem executar de novo"`
}

type deleteBackupOutput struct {
	Body struct {
		Status string `json:"status"`
	}
}

// RenameSessionRequest renames, taking ownership along with it.
type RenameSessionRequest struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type renameSessionInput struct {
	// Renaming twice: the second one 404s, because the source name no longer
	// exists. It is the most retry-hostile case of all the ones here.
	IdemKey string `header:"Idempotency-Key" doc:"Chave gerada pelo aparelho; repetir a mesma chave devolve o resultado da primeira vez, sem executar de novo"`
	Body    RenameSessionRequest
}

type statusOutput struct {
	Body struct {
		Status string `json:"status"`
	}
}

// There is no "detach" route here on purpose. On the dtach backend — the only
// one in use — `Detach` is a no-op (backend_dtach.go): dtach does not keep a list
// of clients to evict, whoever leaves is the process that closes the socket. The
// web panel exposes the button by inheritance from an older engine; repeating that
// in the app would mean shipping a button that does nothing.

// dataDirDe tolerates a nil cfg (see registerTerminalBackup).
func dataDirDe(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	return cfg.DataDir
}

func registerTerminalBackup(api huma.API, deps Deps) {
	cfg := deps.Cfg
	own := deps.SessionOwn
	audit := deps.Audit
	// `cfg` is nil when the OpenAPI generator assembles the registry only to extract
	// the contract (cmd/mobile-openapi-gen). Registering a route must not dereference
	// any dependency — the registry describes, it does not execute.
	store := sessionbackup.New(dataDirDe(cfg))

	huma.Register(api, huma.Operation{
		OperationID: "listTerminalBackups",
		Method:      http.MethodGet,
		Path:        "/terminal/backups",
		Summary:     "Backups de sessão do usuário, mais novos primeiro",
		Tags:        []string{"mobile", "terminal"},
		Middlewares: huma.Middlewares{requireAuth},
		Errors:      []int{http.StatusUnauthorized},
	}, listBackupsHandler(store))

	huma.Register(api, huma.Operation{
		OperationID: "createTerminalBackup",
		Method:      http.MethodPost,
		Path:        "/terminal/backups",
		Summary:     "Salva o estado atual de uma sessão (ou de todas)",
		Tags:        []string{"mobile", "terminal"},
		Middlewares: huma.Middlewares{comAuditoria},
		Errors:      []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusBadRequest},
	}, createBackupHandler(cfg, own, audit, store, deps.Idem))

	huma.Register(api, huma.Operation{
		OperationID: "restoreTerminalBackup",
		Method:      http.MethodPost,
		Path:        "/terminal/backups/restore",
		Summary:     "Recria as sessões de um backup (pula as que já existem)",
		Tags:        []string{"mobile", "terminal"},
		Middlewares: huma.Middlewares{comAuditoria},
		Errors:      []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusBadRequest},
	}, restoreBackupHandler(own, audit, store, deps.Idem))

	huma.Register(api, huma.Operation{
		OperationID: "deleteTerminalBackup",
		Method:      http.MethodDelete,
		Path:        "/terminal/backups/{id}",
		Summary:     "Exclui um backup, ou só uma sessão de dentro dele",
		Tags:        []string{"mobile", "terminal"},
		Middlewares: huma.Middlewares{comAuditoria},
		Errors:      []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusBadRequest},
	}, deleteBackupHandler(audit, store, deps.Idem))

	huma.Register(api, huma.Operation{
		OperationID: "renameTerminalSession",
		Method:      http.MethodPost,
		Path:        "/terminal/sessions/rename",
		Summary:     "Renomeia uma sessão, levando a posse junto",
		Tags:        []string{"mobile", "terminal"},
		Middlewares: huma.Middlewares{comAuditoria},
		Errors:      []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusBadRequest},
	}, renameSessionHandler(cfg, own, audit, deps.Idem))

}

func listBackupsHandler(store *sessionbackup.Store) func(context.Context, *struct{}) (*backupsListOutput, error) {
	return func(ctx context.Context, _ *struct{}) (*backupsListOutput, error) {
		user := auth.UserFromContext(ctx)
		metas := store.List(user)
		out := make([]BackupSummary, 0, len(metas))
		for _, m := range metas {
			sessoes := make([]BackupSessionSummary, 0, len(m.Sessoes))
			for _, s := range m.Sessoes {
				sessoes = append(sessoes, BackupSessionSummary{Name: s.Nome, Summary: s.Resumo, Lines: s.Linhas})
			}
			out = append(out, BackupSummary{
				ID: m.ID, Created: m.Criado, Source: m.Origem, Bytes: m.Bytes, Sessions: sessoes,
			})
		}
		return &backupsListOutput{Body: out}, nil
	}
}

func createBackupHandler(
	cfg *config.Config,
	own *ptysvc.Ownership,
	audit *auth.AuditLog,
	store *sessionbackup.Store,
	idem *Idempotencia,
) func(context.Context, *createBackupInput) (*createBackupOutput, error) {
	return func(ctx context.Context, in *createBackupInput) (*createBackupOutput, error) {
		return lembrarResultado(idem, chaveDoContexto(ctx, in.IdemKey), func() (*createBackupOutput, error) {
			user := auth.UserFromContext(ctx)
			primary := httpx.IsAdmin(cfg, user)

			var nomes []string
			if nome := strings.TrimSpace(in.Body.Name); nome != "" {
				// 404 and not 403: to someone who is not the owner, the session does not exist. A
				// 403 would confirm the name exists in another account.
				if !ptysvc.OwnsSession(user, nome, primary, own) {
					return nil, huma.Error404NotFound("session not found")
				}
				nomes = []string{nome}
			} else {
				sessoes, err := ptysvc.SessionListForUser(user, primary, own)
				if err != nil {
					return nil, huma.Error500InternalServerError("list sessions: " + err.Error())
				}
				for _, s := range sessoes {
					if n, _ := s["name"].(string); n != "" {
						nomes = append(nomes, n)
					}
				}
			}
			if len(nomes) == 0 {
				return nil, huma.Error400BadRequest("no session to back up")
			}

			bk := ptysvc.Backup{
				ID:      strconv.FormatInt(time.Now().UnixNano(), 10),
				Created: time.Now().Unix(),
				Source:  sessionbackup.OrigemManual,
			}
			for _, n := range nomes {
				snap, err := ptysvc.SnapshotSession(n, ptysvc.DefaultScrollbackLines)
				if err != nil {
					continue // the session vanished mid-backup — skip it
				}
				bk.Sessions = append(bk.Sessions, snap)
			}
			if len(bk.Sessions) == 0 {
				return nil, huma.Error500InternalServerError("failed to capture the sessions")
			}
			if err := store.Write(user, bk); err != nil {
				return nil, huma.Error500InternalServerError(err.Error())
			}
			auditar(ctx, audit, user, "terminal.backup", bk.ID)

			out := &createBackupOutput{}
			out.Body = CreateBackupResponse{ID: bk.ID, Created: bk.Created, Count: len(bk.Sessions)}
			return out, nil
		})
	}
}

func restoreBackupHandler(
	own *ptysvc.Ownership,
	audit *auth.AuditLog,
	store *sessionbackup.Store,
	idem *Idempotencia,
) func(context.Context, *restoreBackupInput) (*restoreBackupOutput, error) {
	return func(ctx context.Context, in *restoreBackupInput) (*restoreBackupOutput, error) {
		return lembrarResultado(idem, chaveDoContexto(ctx, in.IdemKey), func() (*restoreBackupOutput, error) {
			user := auth.UserFromContext(ctx)
			if !sessionbackup.IDValido(in.Body.ID) {
				return nil, huma.Error400BadRequest("invalid backup id")
			}
			bk, err := store.Read(user, in.Body.ID)
			if err != nil {
				return nil, huma.Error404NotFound("backup not found")
			}
			dir, err := store.Dir(user)
			if err != nil {
				return nil, huma.Error500InternalServerError(err.Error())
			}
			scratch := filepath.Join(dir, "scratch")
			_ = os.MkdirAll(scratch, 0o700)

			// The quota counts what already exists PLUS what this restore is going to
			// create: restoring a bundle of 7 sessions into an account that already has 5
			// must not breach the limit just because it is a single request.
			existentes := len(own.SessionsOf(user))
			restauradas, puladas := 0, 0
			for _, s := range bk.Sessions {
				if in.Body.Name != "" && s.Name != ptysvc.SafeSessionName(in.Body.Name) {
					continue
				}
				if existentes+restauradas >= ptysvc.MaxSessionsPerUser {
					puladas++
					continue
				}
				if err := ptysvc.RestoreSession(s, scratch); err != nil {
					puladas++
					continue
				}
				// The recreated session belongs to whoever restored it.
				_ = own.Claim(ptysvc.SafeSessionName(s.Name), user)
				restauradas++
			}
			auditar(ctx, audit, user, "terminal.restore", in.Body.ID)

			out := &restoreBackupOutput{}
			out.Body = RestoreBackupResponse{Restored: restauradas, Skipped: puladas}
			return out, nil
		})
	}
}

func deleteBackupHandler(
	audit *auth.AuditLog,
	store *sessionbackup.Store,
	idem *Idempotencia,
) func(context.Context, *deleteBackupInput) (*deleteBackupOutput, error) {
	return func(ctx context.Context, in *deleteBackupInput) (*deleteBackupOutput, error) {
		return lembrarResultado(idem, chaveDoContexto(ctx, in.IdemKey), func() (*deleteBackupOutput, error) {
			user := auth.UserFromContext(ctx)
			if !sessionbackup.IDValido(in.ID) {
				return nil, huma.Error400BadRequest("invalid backup id")
			}
			if err := store.Delete(user, in.ID, in.Name); err != nil {
				if os.IsNotExist(err) {
					return nil, huma.Error404NotFound("backup not found")
				}
				return nil, huma.Error500InternalServerError(err.Error())
			}
			alvo := in.ID
			if in.Name != "" {
				alvo += "#" + ptysvc.SafeSessionName(in.Name)
			}
			auditar(ctx, audit, user, "terminal.backup_delete", alvo)

			out := &deleteBackupOutput{}
			out.Body.Status = "ok"
			return out, nil
		})
	}
}

func renameSessionHandler(
	cfg *config.Config,
	own *ptysvc.Ownership,
	audit *auth.AuditLog,
	idem *Idempotencia,
) func(context.Context, *renameSessionInput) (*statusOutput, error) {
	return func(ctx context.Context, in *renameSessionInput) (*statusOutput, error) {
		return lembrarResultado(idem, chaveDoContexto(ctx, in.IdemKey), func() (*statusOutput, error) {
			user := auth.UserFromContext(ctx)
			de := strings.TrimSpace(in.Body.From)
			para := ptysvc.SafeSessionName(strings.TrimSpace(in.Body.To))
			if de == "" || para == "" {
				return nil, huma.Error400BadRequest("from and to are required")
			}
			if !ptysvc.OwnsSession(user, de, httpx.IsAdmin(cfg, user), own) {
				return nil, huma.Error404NotFound("session not found")
			}
			if err := ptysvc.SessionRename(de, para); err != nil {
				return nil, huma.Error500InternalServerError(err.Error())
			}
			// Ownership follows the name. Without this the renamed session disappears from
			// its own owner's list — the registry would keep pointing at the old name.
			_ = own.Rename(ptysvc.SafeSessionName(de), para)
			auditar(ctx, audit, user, "terminal.rename", de+"→"+para)

			out := &statusOutput{}
			out.Body.Status = "ok"
			return out, nil
		})
	}
}
