package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"server-control-panel/internal/sessions"
)

type ctxKey struct{}
type jtiKey struct{}

// UserFrom returns the username carried in the request context by Middleware.
// Empty string means the request was not authenticated (or the middleware was
// bypassed — callers must not trust an empty value).
func UserFrom(r *http.Request) string {
	return UserFromContext(r.Context())
}

// UserFromContext is UserFrom for callers that only have a context.Context,
// not the *http.Request itself — e.g. huma/v2 typed handlers, whose signature
// is func(context.Context, *Input) (*Output, error) and never exposes the
// raw request. Same source of truth (Middleware sets ctxKey{} on the request
// context before dispatch reaches any handler), no separate auth path.
func UserFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKey{}).(string)
	return v
}

// WithUser injects a username into the context — used in tests to stand in
// for the Middleware, which normally does this after validating the JWT. Do
// not use outside tests (Middleware is the canonical source in production).
func WithUser(ctx context.Context, user string) context.Context {
	return context.WithValue(ctx, ctxKey{}, user)
}

// JTIFrom returns the session id (JWT `jti` claim) of the current request.
// Lets handlers identify which token is being used — e.g. so /logout can
// revoke only the active session.
func JTIFrom(r *http.Request) string {
	v, _ := r.Context().Value(jtiKey{}).(string)
	return v
}

// JTIFromAny returns the JTI from any request, even unauthenticated routes
// (static assets, login page). It tries the context first (in case the request
// already went through Middleware), then extracts from the cookie/header with
// no revocation check. Used by the bandwidth tracker to attribute bytes to a
// session even on routes that never reach Middleware (e.g. '/' static files).
//
// Returns an empty string when there is no token or the JWT is malformed.
// Do NOT use for authentication — it checks neither signature nor revocation.
func (s *Service) JTIFromAny(r *http.Request) string {
	if jti := JTIFrom(r); jti != "" {
		return jti
	}
	tok := extractToken(r)
	if tok == "" {
		return ""
	}
	claims, err := s.ParseClaims(tok)
	if err != nil {
		return ""
	}
	if jti, ok := claims["jti"].(string); ok {
		return jti
	}
	return ""
}

type Credential struct {
	Username     string
	PasswordHash string
}

// AuthBackend selects the password validation policy used by Verify.
//
//   - BackendLocal — local bcrypt from config.json only (legacy behaviour).
//   - BackendSupabase — GoTrue only.
//   - BackendBoth — try GoTrue first; on a network_error, if local bcrypt
//     knows the user, fall back with a WARN log plus a distinct audit entry.
//     This was the default while local bcrypt was still armed.
type AuthBackend string

const (
	BackendLocal    AuthBackend = "local"
	BackendSupabase AuthBackend = "supabase"
	BackendBoth     AuthBackend = "both"
)

// VerifyResult reports which path accepted the credential — useful for the audit log.
type VerifyResult struct {
	OK     bool
	Source string // "local", "supabase", "fallback-local" (supabase down → local)
	Email  string // empty when Source=local
	// Session is populated ONLY when Source=supabase and the password was
	// validated successfully. It carries the refresh_token that logout uses to
	// revoke the session.
	Session *SupabaseSession
}

type Service struct {
	secret   []byte
	usersMu  sync.RWMutex
	users    []Credential
	sessions *sessions.Store // optional — when set, tokens are tracked & revocable

	// Supabase backend (nil when BackendLocal).
	backend  AuthBackend
	supabase *SupabaseClient
	uuidMap  *UUIDMap
}

func New(secret string, users []Credential) *Service {
	return &Service{secret: []byte(secret), users: users, backend: BackendLocal}
}

// WithSupabase wires up the Supabase backend and the uuid map. Called at boot
// when config.json has supabase_url and supabase_anon_key set. backend picks
// the policy; client/uuidMap are its dependencies.
func (s *Service) WithSupabase(backend AuthBackend, client *SupabaseClient, uuidMap *UUIDMap) *Service {
	s.backend = backend
	s.supabase = client
	s.uuidMap = uuidMap
	return s
}

// Backend returns the active policy — exposed for diagnostics on /api/health.
func (s *Service) Backend() AuthBackend {
	return s.backend
}

// ReloadUsers swaps the credential list at runtime. Used by the user
// management APIs (add/remove/reset) — avoids having to restart the process
// on every change.
func (s *Service) ReloadUsers(users []Credential) {
	s.usersMu.Lock()
	s.users = users
	s.usersMu.Unlock()
}

// WithSessions attaches a session store. Once set, every Issue creates a
// session entry and every Middleware request validates against the store.
// Migration: a JWT that's valid by signature but missing from the store gets
// added on first use, so an upgrade doesn't kick everyone out.
func (s *Service) WithSessions(store *sessions.Store) *Service {
	s.sessions = store
	return s
}

// Sessions exposes the session store (or nil when not configured).
func (s *Service) Sessions() *sessions.Store { return s.sessions }

func HashPassword(pass string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pass), 12)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Verify keeps the legacy signature for callers that only need a bool (tests,
// non-handler code). Internally it delegates to VerifyDetailed and discards
// the detail.
func (s *Service) Verify(username, password string) bool {
	res, _ := s.VerifyDetailed(context.Background(), username, password)
	return res.OK
}

// VerifyDetailed runs the active backend's policy and reports which source
// accepted the credential. BackendSupabase is the only live path.
// BackendLocal/BackendBoth stay defined in the enum as an operational kill
// switch (the env var VPSM_AUTH_BACKEND=local re-enables verifyLocal for
// emergency recovery), but the default is Supabase. The audit log
// distinguishes the source.
func (s *Service) VerifyDetailed(ctx context.Context, username, password string) (VerifyResult, error) {
	switch s.backend {
	case BackendLocal:
		// Operational kill switch: VPSM_AUTH_BACKEND=local, for when Supabase
		// is down for an extended period. Local bcrypt login — but since the
		// move to Supabase stripped password_hash out of config.json, this path
		// only works if an operator manually restored a pre-migration
		// config.json.bak.<ts> (procedure documented in the RUNBOOK).
		if s.verifyLocal(username, password) {
			return VerifyResult{OK: true, Source: "local"}, nil
		}
		return VerifyResult{OK: false, Source: "local"}, nil
	case BackendSupabase, BackendBoth, "":
		fallthrough
	default:
		// BackendBoth was collapsed into Supabase-only. It stays in the enum
		// purely for backwards compatibility with older configs — behaviour is
		// identical to Supabase. No automatic bcrypt fallback.
		return s.verifySupabase(ctx, username, password)
	}
}

// verifyLocal — KILL SWITCH ONLY. It is reached only when an operator
// explicitly sets VPSM_AUTH_BACKEND=local (env var in the systemd unit).
// On the normal path this is dead code. Kept for emergency recovery, for
// when Supabase is down AND the operator restored a pre-migration
// config.json.bak.
func (s *Service) verifyLocal(username, password string) bool {
	s.usersMu.RLock()
	users := s.users
	s.usersMu.RUnlock()
	for _, u := range users {
		if subtle.ConstantTimeCompare([]byte(u.Username), []byte(username)) == 1 {
			if u.PasswordHash == "" {
				// The move to Supabase zeroed out the hashes — restoring
				// config.json.bak is the explicit way to re-enable this.
				return false
			}
			return bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) == nil
		}
	}
	return false
}

// verifySupabase validates credentials against GoTrue — the default path.
// Errors are classified; the caller maps them to an HTTP status.
func (s *Service) verifySupabase(ctx context.Context, username, password string) (VerifyResult, error) {
	if s.supabase == nil {
		return VerifyResult{OK: false, Source: "supabase"}, ErrSupabaseUnexpected
	}
	email, _, ok := s.uuidMap.Lookup(username)
	if !ok {
		return VerifyResult{OK: false, Source: "supabase"}, ErrSupabaseInvalidCredentials
	}
	sess, err := s.supabase.VerifyPassword(ctx, email, password)
	if err != nil {
		return VerifyResult{OK: false, Source: "supabase", Email: email}, err
	}
	return VerifyResult{OK: true, Source: "supabase", Email: email, Session: sess}, nil
}

// SupabaseClient returns the active GoTrue client (may be nil under
// BackendLocal). Exposed for handlers that need to revoke a refresh_token
// (logout) or, later, rotate one (refresh).
func (s *Service) SupabaseClient() *SupabaseClient {
	return s.supabase
}

// UUIDMap returns the username↔email/uuid mapping loaded at boot. Used by
// the login handler so the username field also accepts an email (resolving
// email → canonical username before validating), and by /api/auth/me to
// expose the email.
func (s *Service) UUIDMap() *UUIDMap {
	return s.uuidMap
}

func trimUA(ua string) string {
	const max = 200
	if len(ua) > max {
		return ua[:max] + "…"
	}
	return ua
}

// ExtractJTI validates the signature and returns the token's jti. Used by
// videocall.HandleGuestWS to check revocation in InviteSessionsCk once
// VerifyVideocallGuestToken has already validated type and signature.
func (s *Service) ExtractJTI(token string) (string, error) {
	c, err := s.ParseClaims(token)
	if err != nil {
		return "", err
	}
	if jti, ok := c["jti"].(string); ok && jti != "" {
		return jti, nil
	}
	return "", errors.New("no jti")
}

// ParseClaims returns the full claim set so callers can inspect exp/iat.
func (s *Service) ParseClaims(token string) (jwt.MapClaims, error) {
	t, err := jwt.Parse(token, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("bad signing method")
		}
		return s.secret, nil
	})
	if err != nil || !t.Valid {
		return nil, errors.New("invalid token")
	}
	c, ok := t.Claims.(jwt.MapClaims)
	if !ok {
		return nil, errors.New("bad claims")
	}
	return c, nil
}

// Parse validates the token signature and returns the subject. For
// session-aware validation use parseWithJTI internally via Middleware.
func (s *Service) Parse(token string) (string, error) {
	sub, _, err := s.ParseWithJTI(token)
	return sub, err
}

// ParseWithJTI validates the JWT (signature + kind allowlist) and returns
// (sub, jti). Exported for use outside the Middleware — e.g. a forward-auth
// handler that has to choose between 200-with-header and 200-without-header
// without failing the request with a 401.
//
// SECURITY: rejects any token whose kind is neither "" nor "session".
// Without this, a vpsm_recovery_token / videocall_invite / videocall_guest
// (all signed with the same HMAC secret) would authenticate against any
// /api/*. Issuing and verifying non-session tokens must go through dedicated
// helpers (verifyKindToken) that check the kind explicitly.
func (s *Service) ParseWithJTI(token string) (sub, jti string, err error) {
	t, perr := jwt.Parse(token, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("bad signing method")
		}
		return s.secret, nil
	})
	if perr != nil || !t.Valid {
		return "", "", errors.New("invalid token")
	}
	claims, ok := t.Claims.(jwt.MapClaims)
	if !ok {
		return "", "", errors.New("bad claims")
	}
	// Kind allowlist: a token with no kind (legacy, sub-only) or with
	// kind="session" is valid. Anything else (recovery/setup/
	// videocall_invite/videocall_guest) is rejected here — those tokens have
	// dedicated flows (handleRecoveryAuth, InviteConsume, etc.) that do not
	// go through the general Middleware.
	if k, _ := claims["kind"].(string); k != "" && k != "session" {
		return "", "", errors.New("invalid token kind")
	}
	sub, _ = claims["sub"].(string)
	jti, _ = claims["jti"].(string)
	return sub, jti, nil
}

func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// WebSocket handshake with a one-shot ticket (?ticket=<X>).
		//
		// WHY a ticket instead of putting the JWT in the query string: the WS
		// upgrade carries no Authorization header, and the native app has no
		// browser cookie — that leaves the URL. But URLs leak: into proxy access
		// logs, into Referer, into the client's history, and into any tracing
		// along the way. A session JWT is good for 12h; leaked there, it is good
		// for 12h to whoever finds it. The ticket is the trade: 60s lifetime,
		// single use (ConsumeWSTicket is atomic), and bound to the user/session
		// that asked for it.
		//
		// WHY resolve it HERE and not in each /ws/ handler: the Middleware
		// answers 401 BEFORE any handler runs, so a handler that understood
		// tickets on its own would never be reached — that was exactly the
		// /ws/shell bug (the app asked for a ticket, built the right URL, and
		// got a 401 from the middleware). One place covers every /ws/ route,
		// present and future, with no need to mount a route outside the
		// middleware — an exception that, once repeated, one day leaves a route
		// with no authentication at all.
		if user, jti, ok := s.wsTicketAuth(r); ok {
			next.ServeHTTP(w, r.WithContext(withIdentity(r.Context(), user, jti)))
			return
		}
		token := extractToken(r)
		if token == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		sub, jti, err := s.ParseWithJTI(token)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		// Session-store check. Three cases:
		//   1. Touch succeeds → live session, fast path.
		//   2. Touch fails AND HasTombstone → revoked on purpose, reject.
		//   3. Touch fails AND !HasTombstone → unknown JTI, migrate by Add()
		//      (covers tokens minted before the sessions feature shipped, or
		//      a restart that lost an unflushed write).
		if s.sessions != nil && jti != "" {
			if !s.sessions.Touch(jti) {
				if s.sessions.HasTombstone(jti) {
					http.Error(w, "session revoked", http.StatusUnauthorized)
					return
				}
				claims, _ := s.ParseClaims(token)
				exp, _ := claims["exp"].(float64)
				iat, _ := claims["iat"].(float64)
				s.sessions.Add(sessions.Session{
					JTI:       jti,
					User:      sub,
					IP:        ClientIP(r),
					UserAgent: trimUA(r.Header.Get("User-Agent")),
					IssuedAt:  int64(iat),
					LastSeen:  time.Now().Unix(),
					ExpiresAt: int64(exp),
				})
			}
		}
		next.ServeHTTP(w, r.WithContext(withIdentity(r.Context(), sub, jti)))
	})
}

// withIdentity stamps onto the context the identity every handler later reads
// via UserFrom/JTIFrom (and that huma reads via UserFromContext). It exists as
// a single helper because the Middleware has two authentication paths — JWT
// and WS ticket — and they MUST hand downstream exactly the same keys in the
// same format. Diverging here is the kind of silent defect where the handshake
// passes and the handler further along sees an empty user (or the wrong
// owner).
func withIdentity(ctx context.Context, user, jti string) context.Context {
	ctx = context.WithValue(ctx, ctxKey{}, user)
	return context.WithValue(ctx, jtiKey{}, jti)
}

// wsTicketAuth resolves the one-shot ticket of a WebSocket handshake
// (?ticket=<X>) and returns the (user, jti) pair the ticket carries — the same
// pair the JWT path extracts from the token.
//
// Only valid on /ws/ paths: outside the handshake there is no reason to accept
// a ticket, and checking here guarantees that no /api/ route starts accepting
// one by accident (a ticket is a handshake credential, not a session one).
//
// ok=false when there is no ticket, when the ticket is not valid (unknown,
// already consumed, or expired), or when the session that issued it was
// revoked inside the validity window. In those cases the caller falls through
// to the normal path (Bearer / cookie / ?token=), which decides the 401 — a
// bad ticket never authenticates on its own, and it never fails a request that
// already carried a legitimate credential alongside it.
func (s *Service) wsTicketAuth(r *http.Request) (user, jti string, ok bool) {
	if !strings.HasPrefix(r.URL.Path, "/ws/") {
		return "", "", false
	}
	t := r.URL.Query().Get("ticket")
	if t == "" {
		return "", "", false
	}
	u, j, valid := ConsumeWSTicket(t)
	if !valid {
		return "", "", false
	}
	// The ticket inherits the fate of the session that issued it: a logout or
	// revoke-sessions inside those 60s kills the ticket with it. Without this
	// it would be an orphan credential outliving the logout that should have
	// killed it. Touch failing WITHOUT a tombstone means an unknown jti (a
	// token older than the store, or a restart that lost an unflushed write) —
	// the same leniency as the JWT path, which in that case migrates the
	// session instead of rejecting it.
	if s != nil && s.sessions != nil && j != "" {
		if !s.sessions.Touch(j) && s.sessions.HasTombstone(j) {
			return "", "", false
		}
	}
	return u, j, true
}

func extractToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	// ?token=<JWT> is allowed only on /ws/ paths — a WebSocket upgrade sends
	// no Authorization header. On any other path the token would leak through
	// Referer, access logs, and browser history. Restricting it closes that
	// vector without hurting the legitimate case.
	if strings.HasPrefix(r.URL.Path, "/ws/") {
		if t := r.URL.Query().Get("token"); t != "" {
			return t
		}
	}
	if c, err := r.Cookie("vpsm_token"); err == nil {
		return c.Value
	}
	return ""
}

// ExtractWSAuth resolves user/jti for a WS upgrade. It accepts three routes,
// in order of preference: one-shot ticket, HttpOnly cookie, ?token=. Returns
// an empty user when none of them authenticates.
//
// Ticket via ?ticket=<X>: one-shot, 60s, and does NOT log a JWT into access
// logs. This is the recommended path for new frontend code.
func (s *Service) ExtractWSAuth(r *http.Request) (user, jti string) {
	// Prefer the ticket (safer). Same function the Middleware uses — the
	// ticket semantics (one-shot, 60s, dies with the session that issued it,
	// /ws/ only) live in exactly one place and must not diverge between the
	// two doors.
	if u, j, ok := s.wsTicketAuth(r); ok {
		return u, j
	}
	// Fallback: cookie or legacy token. Validates the JWT.
	tok := extractToken(r)
	if tok == "" {
		return "", ""
	}
	u, j, err := s.ParseWithJTI(tok)
	if err != nil {
		return "", ""
	}
	return u, j
}
