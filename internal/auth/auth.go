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

func UserFrom(r *http.Request) string {
	return UserFromContext(r.Context())
}

func UserFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKey{}).(string)
	return v
}

func WithUser(ctx context.Context, user string) context.Context {
	return context.WithValue(ctx, ctxKey{}, user)
}

func JTIFrom(r *http.Request) string {
	v, _ := r.Context().Value(jtiKey{}).(string)
	return v
}

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

type AuthBackend string

const (
	BackendLocal    AuthBackend = "local"
	BackendSupabase AuthBackend = "supabase"
	BackendBoth     AuthBackend = "both"
)

type VerifyResult struct {
	OK      bool
	Source  string
	Email   string
	Session *SupabaseSession
}

type Service struct {
	secret   []byte
	usersMu  sync.RWMutex
	users    []Credential
	sessions *sessions.Store

	backend  AuthBackend
	supabase *SupabaseClient
	uuidMap  *UUIDMap
}

func New(secret string, users []Credential) *Service {
	return &Service{secret: []byte(secret), users: users, backend: BackendLocal}
}

func (s *Service) WithSupabase(backend AuthBackend, client *SupabaseClient, uuidMap *UUIDMap) *Service {
	s.backend = backend
	s.supabase = client
	s.uuidMap = uuidMap
	return s
}

func (s *Service) Backend() AuthBackend {
	return s.backend
}

func (s *Service) ReloadUsers(users []Credential) {
	s.usersMu.Lock()
	s.users = users
	s.usersMu.Unlock()
}

func (s *Service) WithSessions(store *sessions.Store) *Service {
	s.sessions = store
	return s
}

func (s *Service) Sessions() *sessions.Store { return s.sessions }

func HashPassword(pass string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pass), 12)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (s *Service) Verify(username, password string) bool {
	res, _ := s.VerifyDetailed(context.Background(), username, password)
	return res.OK
}

func (s *Service) VerifyDetailed(ctx context.Context, username, password string) (VerifyResult, error) {
	switch s.backend {
	case BackendLocal:
		if s.verifyLocal(username, password) {
			return VerifyResult{OK: true, Source: "local"}, nil
		}
		return VerifyResult{OK: false, Source: "local"}, nil
	case BackendSupabase, BackendBoth, "":
		fallthrough
	default:
		return s.verifySupabase(ctx, username, password)
	}
}

func (s *Service) verifyLocal(username, password string) bool {
	s.usersMu.RLock()
	users := s.users
	s.usersMu.RUnlock()
	for _, u := range users {
		if subtle.ConstantTimeCompare([]byte(u.Username), []byte(username)) == 1 {
			if u.PasswordHash == "" {
				return false
			}
			return bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) == nil
		}
	}
	return false
}

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

func (s *Service) SupabaseClient() *SupabaseClient {
	return s.supabase
}

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

func (s *Service) Parse(token string) (string, error) {
	sub, _, err := s.ParseWithJTI(token)
	return sub, err
}

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
	if k, _ := claims["kind"].(string); k != "" && k != "session" {
		return "", "", errors.New("invalid token kind")
	}
	sub, _ = claims["sub"].(string)
	jti, _ = claims["jti"].(string)
	return sub, jti, nil
}

func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func withIdentity(ctx context.Context, user, jti string) context.Context {
	ctx = context.WithValue(ctx, ctxKey{}, user)
	return context.WithValue(ctx, jtiKey{}, jti)
}

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
	if strings.HasPrefix(r.URL.Path, "/ws/") {
		if t := r.URL.Query().Get("token"); t != "" {
			return t
		}
	}
	if c, err := r.Cookie("panel_token"); err == nil {
		return c.Value
	}
	return ""
}

func (s *Service) ExtractWSAuth(r *http.Request) (user, jti string) {
	if u, j, ok := s.wsTicketAuth(r); ok {
		return u, j
	}
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
