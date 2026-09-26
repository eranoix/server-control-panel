package auth

// tokens.go — issuing and validating JWTs (vps-manager plus auxiliary tokens)
//
// Tokens covered here:
//   - Main JWT (Issue; with IssueMeta -> records the session in the store)
//   - WS one-shot (IssueWSTicket/ConsumeWSTicket — keeps the JWT out of the query)
//   - Setup token (IssueSetupToken/VerifySetupToken — 5min, mandatory 2FA enrollment)
//   - Recovery token (IssueRecoveryToken/VerifyRecoveryToken — /recovery flow)
//   - Videocall invite + guest tokens (Issue/Verify — magic link + guest entry)
//
// Every Verify* delegates to verifyKindToken (generic kind-claim validation).
// trimUA truncates User-Agent string.
//
// Extracted from auth.go (keeps the same *Service receiver where applicable).

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"server-control-panel/internal/sessions"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TokenTTL is the lifetime of a freshly issued JWT.
const TokenTTL = 12 * time.Hour

// WSTicketTTL is the deliberately short validity of a one-shot WS ticket. 60s
// is enough for the frontend to request the ticket and open the WS right away,
// yet it drastically limits how long the ticket is exposed if it leaks into a
// log.
const WSTicketTTL = 60 * time.Second

// wsTicket is an ephemeral token for authenticating a WebSocket without
// exposing the main JWT in the query string. The frontend trades its HttpOnly
// cookie for a ticket via /api/auth/ws-ticket; the WS accepts ?ticket=<X> and
// consumes it (one-shot).
type wsTicket struct {
	user      string
	jti       string
	expiresAt time.Time
}

// wsTicketStore holds the live tickets. Concurrency-safe; expired tickets are
// garbage-collected on every Issue.
type wsTicketStore struct {
	mu      sync.Mutex
	tickets map[string]wsTicket
}

var globalTicketStore = &wsTicketStore{tickets: make(map[string]wsTicket)}

// IssueWSTicket mints a one-shot ticket bound to (user, jti). Returns the
// ticket string. The ticket MUST be consumed via ConsumeWSTicket within
// WSTicketTTL.
func IssueWSTicket(user, jti string) string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	t := hex.EncodeToString(b)
	now := time.Now()
	globalTicketStore.mu.Lock()
	defer globalTicketStore.mu.Unlock()
	// GC expired entries so we don't leak memory under load
	for k, v := range globalTicketStore.tickets {
		if now.After(v.expiresAt) {
			delete(globalTicketStore.tickets, k)
		}
	}
	globalTicketStore.tickets[t] = wsTicket{
		user:      user,
		jti:       jti,
		expiresAt: now.Add(WSTicketTTL),
	}
	return t
}

// ConsumeWSTicket validates and removes the ticket atomically. Returns
// (user, jti) and ok=true when valid. One-shot: the same ticket can never be
// used again.
func ConsumeWSTicket(t string) (user, jti string, ok bool) {
	if t == "" {
		return "", "", false
	}
	globalTicketStore.mu.Lock()
	defer globalTicketStore.mu.Unlock()
	v, exists := globalTicketStore.tickets[t]
	if !exists {
		return "", "", false
	}
	delete(globalTicketStore.tickets, t)
	if time.Now().After(v.expiresAt) {
		return "", "", false
	}
	return v.user, v.jti, true
}

// PairingTicketTTL is the validity of the QR-code pairing ticket.
// 5 minutes is enough time for the user to scan the QR on a
// new phone with no session at all; short enough that a QR photographed
// by accident (screenshot, print) does not stay useful for long. The ticket
// itself is NEVER enough to obtain a session — see ConsumePairingTicket.
const PairingTicketTTL = 5 * time.Minute

// PairingEnvelopeVersion identifies the format of the payload encoded in the
// pairing QR code. The Android app rejects any `v` it does not recognise
// instead of trying to interpret fields that may not exist — see
// `PairingClient.parsePairingPayload` (android/data/.../PairingClient.kt).
// Bump this value only when the format changes in a way that breaks an older
// parser (a field removed or renamed); adding a new, optional field does not
// require a bump.
const PairingEnvelopeVersion = 1

// PairingEnvelope is the exact content (JSON-serialised) of the pairing QR
// code — the same text the phone's camera decodes. It carries both the
// one-shot ticket AND the target server (Config.PublicHostname), so the new
// app does not need the user to type the server address by hand (see
// ServerSetupScreen, which becomes only the fallback path). ServerURL is never
// by itself an authorization to repoint an app that is already paired — that
// decision belongs to ServerConfigRepository.configure (Kotlin), which refuses
// a repoint absent an explicit user action.
type PairingEnvelope struct {
	V         int    `json:"v"`
	Ticket    string `json:"ticket"`
	ServerURL string `json:"server_url"`
	ExpiresIn int    `json:"expires_in"`
}

// pairingTicket is an ephemeral, one-shot token that authorizes exactly ONE
// exchange for a webauthn_reg token (via ConsumePairingTicket) — never a
// session. Same shape as wsTicket, with a longer TTL because the user needs
// time to open the camera on the new phone and scan the QR shown by the
// already-authenticated device.
type pairingTicket struct {
	user      string
	expiresAt time.Time
}

type pairingTicketStore struct {
	mu      sync.Mutex
	tickets map[string]pairingTicket
}

var globalPairingTicketStore = &pairingTicketStore{tickets: make(map[string]pairingTicket)}

// IssuePairingTicket mints a one-shot ticket bound to `username` — the owner
// of the already-authenticated session that started the pairing (never a
// username supplied by the client; see handleMobilePairStart). Returns the
// ticket in the clear; it travels only inside the QR code shown in the panel.
func IssuePairingTicket(username string) string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	t := hex.EncodeToString(b)
	now := time.Now()
	globalPairingTicketStore.mu.Lock()
	defer globalPairingTicketStore.mu.Unlock()
	for k, v := range globalPairingTicketStore.tickets {
		if now.After(v.expiresAt) {
			delete(globalPairingTicketStore.tickets, k)
		}
	}
	globalPairingTicketStore.tickets[t] = pairingTicket{
		user:      username,
		expiresAt: now.Add(PairingTicketTTL),
	}
	return t
}

// ConsumePairingTicket validates and removes the ticket atomically — one-shot:
// it can never be reused, not even inside the validity window, not even if the
// exchange that follows (register/begin) fails. A missing, expired, or
// already-consumed ticket all return ok=false, without distinguishing which of
// the three it was: a replay must never "almost work".
func ConsumePairingTicket(t string) (username string, ok bool) {
	if t == "" {
		return "", false
	}
	globalPairingTicketStore.mu.Lock()
	defer globalPairingTicketStore.mu.Unlock()
	v, exists := globalPairingTicketStore.tickets[t]
	if !exists {
		return "", false
	}
	delete(globalPairingTicketStore.tickets, t)
	if time.Now().After(v.expiresAt) {
		return "", false
	}
	return v.user, true
}

// Issue mints a JWT for `username`. When a sessions store is attached, it
// also records a Session row (with the IP/UA pulled from `meta`) so the user
// can later see and revoke this specific token.
//
// `meta` may be nil for callers that don't have a request handy (e.g. tests).
// Returns (token, jti, error).
func (s *Service) Issue(username string, meta *IssueMeta) (string, string, error) {
	now := time.Now()
	jb := make([]byte, 8)
	_, _ = rand.Read(jb)
	jti := hex.EncodeToString(jb)
	exp := now.Add(TokenTTL).Unix()
	claims := jwt.MapClaims{
		"sub": username,
		"exp": exp,
		"iat": now.Unix(),
		"jti": jti,
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tok, err := t.SignedString(s.secret)
	if err != nil {
		return "", "", err
	}
	if s.sessions != nil {
		sess := sessions.Session{
			JTI:       jti,
			User:      username,
			IssuedAt:  now.Unix(),
			LastSeen:  now.Unix(),
			ExpiresAt: exp,
		}
		if meta != nil {
			sess.IP = meta.IP
			sess.UserAgent = trimUA(meta.UserAgent)
		}
		s.sessions.Add(sess)
	}
	return tok, jti, nil
}

// IssueMeta carries the request-bound metadata captured when a session is
// minted. Optional but recommended for sessions UX.
type IssueMeta struct {
	IP        string
	UserAgent string
}

// IssueSetupToken mints a SHORT-LIVED token used ONLY during first-login TOTP
// enrollment. The token carries claim kind="setup" so it can never be used to
// access protected APIs — only the dedicated /api/auth/setup-enroll and
// /api/auth/setup-confirm endpoints accept it. ttl should be small (5 min).
//
// No session row is recorded; the token is single-purpose and the real session
// is minted by setup-confirm once both TOTPs are validated.
func (s *Service) IssueSetupToken(username string, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"sub":  username,
		"exp":  now.Add(ttl).Unix(),
		"iat":  now.Unix(),
		"kind": "setup",
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return t.SignedString(s.secret)
}

// VerifySetupToken parses a setup token and returns the username, or an error
// if invalid, expired, or wrong kind.
func (s *Service) VerifySetupToken(token string) (string, error) {
	return s.verifyKindToken(token, "setup")
}

// IssueRecoveryToken mints a session token for the /recovery flow. Carries
// claim kind="recovery" so it's never accepted by the protected.HandleFunc
// middleware (which only accepts kind unset or kind="session"). Stored in
// HttpOnly cookie `vpsm_recovery_token`.
func (s *Service) IssueRecoveryToken(username string, ttl time.Duration) (string, error) {
	return s.IssueRecoveryTokenFrom(username, ttl, time.Now())
}

// IssueRecoveryTokenFrom issues the token preserving the instant of the original
// LOGIN in `ini`.
//
// The recovery session lasts 30 minutes on purpose: it is a privileged
// path with its own authentication. But expiring in the middle of a repair —
// which is what happened on the first real session — is the worst possible moment
// to demand password + TOTP again. The way out is to renew while work is
// happening, and it is `ini` that keeps this honest: it is NOT reset on every
// renewal, so there is an absolute ceiling counted from login. Whoever is working
// is not interrupted; a forgotten tab still dies.
func (s *Service) IssueRecoveryTokenFrom(username string, ttl time.Duration, ini time.Time) (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"sub":  username,
		"exp":  now.Add(ttl).Unix(),
		"iat":  now.Unix(),
		"ini":  ini.Unix(),
		"kind": "recovery",
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return t.SignedString(s.secret)
}

// RecoveryTokenStart returns when the recovery LOGIN happened — not when the
// current token was issued. Older tokens (minted before this claim existed) do
// not carry it; for those, `iat` applies, which for them is the same thing.
func (s *Service) RecoveryTokenStart(token string) (time.Time, error) {
	t, err := jwt.Parse(token, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return time.Time{}, errors.New("bad signing method")
		}
		return s.secret, nil
	})
	if err != nil {
		return time.Time{}, err
	}
	claims, ok := t.Claims.(jwt.MapClaims)
	if !ok || !t.Valid {
		return time.Time{}, errors.New("invalid token")
	}
	if v, ok := claims["ini"].(float64); ok && v > 0 {
		return time.Unix(int64(v), 0), nil
	}
	if v, ok := claims["iat"].(float64); ok && v > 0 {
		return time.Unix(int64(v), 0), nil
	}
	return time.Time{}, errors.New("no start instant")
}

// VerifyRecoveryToken parses a recovery cookie and returns the username.
func (s *Service) VerifyRecoveryToken(token string) (string, error) {
	return s.verifyKindToken(token, "recovery")
}

// IssueVideocallInviteToken mints a magic-link token for /api/videocall/join
// (public endpoint). Carries claim kind="videocall_invite" so it CAN'T be
// used for anything else, plus a room_id custom claim that the consumer
// uses to add the invitee as a member. Single-use is enforced by tombstoning
// the jti in the sessions store on consumption (see handlers).
func (s *Service) IssueVideocallInviteToken(inviter, roomID string, ttl time.Duration) (token, jti string, err error) {
	now := time.Now()
	jb := make([]byte, 8)
	_, _ = rand.Read(jb)
	jti = hex.EncodeToString(jb)
	claims := jwt.MapClaims{
		"sub":     inviter, // who minted the invite — for audit
		"exp":     now.Add(ttl).Unix(),
		"iat":     now.Unix(),
		"jti":     jti,
		"kind":    "videocall_invite",
		"room_id": roomID,
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	token, err = t.SignedString(s.secret)
	return
}

// IssueVideocallGuestToken mints a JWT for an ANONYMOUS guest who joined via
// PIN. Unlike invite_token: the guest may use this token repeatedly (until it
// expires), but the token grants NO access to other panel routes — only to the
// /ws/videocall of the room embedded in it. displayName is what the other
// peers see in their UI.
func (s *Service) IssueVideocallGuestToken(displayName, roomID string, ttl time.Duration) (token, jti string, err error) {
	now := time.Now()
	jb := make([]byte, 8)
	_, _ = rand.Read(jb)
	jti = hex.EncodeToString(jb)
	if displayName == "" {
		displayName = "Convidado"
	}
	claims := jwt.MapClaims{
		"sub":     "guest:" + displayName, // the prefix tells it apart from a real user
		"exp":     now.Add(ttl).Unix(),
		"iat":     now.Unix(),
		"jti":     jti,
		"kind":    "videocall_guest",
		"room_id": roomID,
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	token, err = t.SignedString(s.secret)
	return
}

// VerifyVideocallGuestToken returns the roomID and displayName when the token
// is valid. Reused by the videocall WS handler — outside it, no route accepts
// this kind.
func (s *Service) VerifyVideocallGuestToken(token string) (roomID, displayName string, err error) {
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
	if kind, _ := claims["kind"].(string); kind != "videocall_guest" {
		return "", "", errors.New("not a videocall_guest token")
	}
	roomID, _ = claims["room_id"].(string)
	sub, _ := claims["sub"].(string)
	displayName = strings.TrimPrefix(sub, "guest:")
	if roomID == "" {
		return "", "", errors.New("missing room_id")
	}
	return roomID, displayName, nil
}

// VerifyVideocallInviteToken parses an invite token. Returns the room_id
// the invite grants access to, plus the jti so the handler can check the
// single-use tombstone and then revoke after consumption.
func (s *Service) VerifyVideocallInviteToken(token string) (roomID, jti string, err error) {
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
	if kind, _ := claims["kind"].(string); kind != "videocall_invite" {
		return "", "", errors.New("not a videocall_invite token")
	}
	roomID, _ = claims["room_id"].(string)
	jti, _ = claims["jti"].(string)
	if roomID == "" {
		return "", "", errors.New("missing room_id")
	}
	return roomID, jti, nil
}

// ---- WebAuthn (passkey) ceremony tokens ----
//
// Registration and discoverable login pass a SessionData (challenge + RP
// binding) between a Begin* and a Finish* HTTP call. Instead of caching that
// value server-side under an opaque session id (which would need its own store
// plus GC), it travels inside a short-lived JWT, HS256-signed and tagged with
// a "kind" — the same pattern IssueSetupToken already uses for the TOTP
// enrollment ceremony. verifyKindToken (called indirectly here) rejects any
// other kind, so none of these tokens is ever accepted by a protected route
// (auth.Service.ParseWithJTI only accepts kind=="" / "session").
//
// Two properties that exp and kind alone do not provide, and that a WebAuthn
// challenge requires — a replay has to fail:
//   - single-use: webauthnUsedJTI consumes the jti (marks it and never accepts
//     it again) on the first verification — replaying the same Finish request
//     a second time fails even with the JWT still inside its validity and
//     correctly signed.
//   - per-ceremony binding: every Issue* generates a fresh jti — a token minted
//     for one ceremony never authorizes another.
const webAuthnCeremonyTTL = 2 * time.Minute

type usedJTIStore struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

var webauthnUsedJTI = &usedJTIStore{seen: make(map[string]time.Time)}

// consume returns true ONLY the first time it sees `jti` (and records it); on
// every later call, until the entry expires naturally out of the map, it
// returns false.
func (u *usedJTIStore) consume(jti string, ttl time.Duration) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	now := time.Now()
	for k, exp := range u.seen {
		if now.After(exp) {
			delete(u.seen, k)
		}
	}
	if _, exists := u.seen[jti]; exists {
		return false
	}
	u.seen[jti] = now.Add(ttl)
	return true
}

// IssueWebAuthnRegToken issues the token authorizing ONE passkey registration
// ceremony for `username` — QR-code pairing mints this token only after
// validating the pairing ticket, and it authorizes exactly one call to
// register/begin (kind "webauthn_reg", single-use).
func (s *Service) IssueWebAuthnRegToken(username string) (string, error) {
	return s.issueCeremonyToken(username, "webauthn_reg", nil)
}

// VerifyWebAuthnRegToken verifies and CONSUMES (single-use) a registration
// authorization token, returning the username it authorizes.
func (s *Service) VerifyWebAuthnRegToken(token string) (string, error) {
	sub, _, err := s.verifyCeremonyToken(token, "webauthn_reg")
	return sub, err
}

// IssueWebAuthnRegSessionToken is issued by register/begin itself: it carries
// the username AND the webauthn.SessionData (JSON-serialised by the caller)
// returned by BeginRegistrationForUser, so that register/finish can rebuild
// the exact ceremony with no server-side cache. Kind "webauthn_reg_session",
// single-use.
func (s *Service) IssueWebAuthnRegSessionToken(username string, sessionJSON []byte) (string, error) {
	return s.issueCeremonyToken(username, "webauthn_reg_session", sessionJSON)
}

// VerifyWebAuthnRegSessionToken verifies and consumes the token above,
// returning the username plus the original serialised SessionData.
func (s *Service) VerifyWebAuthnRegSessionToken(token string) (username string, sessionJSON []byte, err error) {
	return s.verifyCeremonyToken(token, "webauthn_reg_session")
}

// IssueWebAuthnLoginSessionToken is issued by login/begin. A discoverable
// login does not know the username yet (that is the whole point of the flow),
// so this token carries only the SessionData. Kind "webauthn_login_session",
// single-use.
func (s *Service) IssueWebAuthnLoginSessionToken(sessionJSON []byte) (string, error) {
	return s.issueCeremonyToken("", "webauthn_login_session", sessionJSON)
}

// VerifyWebAuthnLoginSessionToken verifies and consumes the login token,
// returning the original serialised SessionData.
func (s *Service) VerifyWebAuthnLoginSessionToken(token string) (sessionJSON []byte, err error) {
	_, sessionJSON, err = s.verifyCeremonyToken(token, "webauthn_login_session")
	return sessionJSON, err
}

func (s *Service) issueCeremonyToken(username, kind string, payload []byte) (string, error) {
	now := time.Now()
	jb := make([]byte, 12)
	_, _ = rand.Read(jb)
	jti := hex.EncodeToString(jb)
	claims := jwt.MapClaims{
		"exp":  now.Add(webAuthnCeremonyTTL).Unix(),
		"iat":  now.Unix(),
		"jti":  jti,
		"kind": kind,
	}
	if username != "" {
		claims["sub"] = username
	}
	if payload != nil {
		claims["data"] = base64.StdEncoding.EncodeToString(payload)
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return t.SignedString(s.secret)
}

func (s *Service) verifyCeremonyToken(token, expectKind string) (username string, payload []byte, err error) {
	t, perr := jwt.Parse(token, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("bad signing method")
		}
		return s.secret, nil
	})
	if perr != nil || !t.Valid {
		return "", nil, errors.New("invalid token")
	}
	claims, ok := t.Claims.(jwt.MapClaims)
	if !ok {
		return "", nil, errors.New("bad claims")
	}
	if kind, _ := claims["kind"].(string); kind != expectKind {
		return "", nil, errors.New("not a " + expectKind + " token")
	}
	jti, _ := claims["jti"].(string)
	if jti == "" || !webauthnUsedJTI.consume(jti, webAuthnCeremonyTTL) {
		return "", nil, errors.New("token already used or invalid")
	}
	username, _ = claims["sub"].(string)
	if raw, ok := claims["data"].(string); ok && raw != "" {
		payload, err = base64.StdEncoding.DecodeString(raw)
		if err != nil {
			return "", nil, errors.New("bad payload encoding")
		}
	}
	return username, payload, nil
}

func (s *Service) verifyKindToken(token, expectKind string) (string, error) {
	t, err := jwt.Parse(token, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("bad signing method")
		}
		return s.secret, nil
	})
	if err != nil {
		return "", err
	}
	claims, ok := t.Claims.(jwt.MapClaims)
	if !ok || !t.Valid {
		return "", errors.New("invalid token")
	}
	if kind, _ := claims["kind"].(string); kind != expectKind {
		return "", errors.New("not a " + expectKind + " token")
	}
	sub, _ := claims["sub"].(string)
	if sub == "" {
		return "", errors.New("missing subject")
	}
	return sub, nil
}
