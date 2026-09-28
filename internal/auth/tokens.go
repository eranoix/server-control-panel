package auth

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

const TokenTTL = 12 * time.Hour

const WSTicketTTL = 60 * time.Second

type wsTicket struct {
	user      string
	jti       string
	expiresAt time.Time
}

type wsTicketStore struct {
	mu      sync.Mutex
	tickets map[string]wsTicket
}

var globalTicketStore = &wsTicketStore{tickets: make(map[string]wsTicket)}

func IssueWSTicket(user, jti string) string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	t := hex.EncodeToString(b)
	now := time.Now()
	globalTicketStore.mu.Lock()
	defer globalTicketStore.mu.Unlock()
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

const PairingTicketTTL = 5 * time.Minute

const PairingEnvelopeVersion = 1

type PairingEnvelope struct {
	V         int    `json:"v"`
	Ticket    string `json:"ticket"`
	ServerURL string `json:"server_url"`
	ExpiresIn int    `json:"expires_in"`
}

type pairingTicket struct {
	user      string
	expiresAt time.Time
}

type pairingTicketStore struct {
	mu      sync.Mutex
	tickets map[string]pairingTicket
}

var globalPairingTicketStore = &pairingTicketStore{tickets: make(map[string]pairingTicket)}

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

type IssueMeta struct {
	IP        string
	UserAgent string
}

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

func (s *Service) VerifySetupToken(token string) (string, error) {
	return s.verifyKindToken(token, "setup")
}

func (s *Service) IssueRecoveryToken(username string, ttl time.Duration) (string, error) {
	return s.IssueRecoveryTokenFrom(username, ttl, time.Now())
}

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

func (s *Service) VerifyRecoveryToken(token string) (string, error) {
	return s.verifyKindToken(token, "recovery")
}

func (s *Service) IssueVideocallInviteToken(inviter, roomID string, ttl time.Duration) (token, jti string, err error) {
	now := time.Now()
	jb := make([]byte, 8)
	_, _ = rand.Read(jb)
	jti = hex.EncodeToString(jb)
	claims := jwt.MapClaims{
		"sub":     inviter,
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

func (s *Service) IssueVideocallGuestToken(displayName, roomID string, ttl time.Duration) (token, jti string, err error) {
	now := time.Now()
	jb := make([]byte, 8)
	_, _ = rand.Read(jb)
	jti = hex.EncodeToString(jb)
	if displayName == "" {
		displayName = "Guest"
	}
	claims := jwt.MapClaims{
		"sub":     "guest:" + displayName,
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

const webAuthnCeremonyTTL = 2 * time.Minute

type usedJTIStore struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

var webauthnUsedJTI = &usedJTIStore{seen: make(map[string]time.Time)}

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

func (s *Service) IssueWebAuthnRegToken(username string) (string, error) {
	return s.issueCeremonyToken(username, "webauthn_reg", nil)
}

func (s *Service) VerifyWebAuthnRegToken(token string) (string, error) {
	sub, _, err := s.verifyCeremonyToken(token, "webauthn_reg")
	return sub, err
}

func (s *Service) IssueWebAuthnRegSessionToken(username string, sessionJSON []byte) (string, error) {
	return s.issueCeremonyToken(username, "webauthn_reg_session", sessionJSON)
}

func (s *Service) VerifyWebAuthnRegSessionToken(token string) (username string, sessionJSON []byte, err error) {
	return s.verifyCeremonyToken(token, "webauthn_reg_session")
}

func (s *Service) IssueWebAuthnLoginSessionToken(sessionJSON []byte) (string, error) {
	return s.issueCeremonyToken("", "webauthn_login_session", sessionJSON)
}

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
