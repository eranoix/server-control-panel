package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pquerna/otp/totp"
	qrcode "github.com/skip2/go-qrcode"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/config"
	"server-control-panel/internal/scope"
)

func trimUA(ua string) string {
	ua = strings.TrimSpace(ua)
	const max = 120
	if len(ua) > max {
		return ua[:max] + "…"
	}
	return ua
}

type mfaCheckResult int

const (
	mfaNotRequired mfaCheckResult = iota
	mfaUnavailableDenied
	mfaTrustedDevice
	mfaCodeRequired
	mfaCodeVerified
	mfaBackupUsed
	mfaCodeInvalid
)

type mfaCheckOutcome struct {
	result   mfaCheckResult
	required bool
	factorID string
}

func (r *Router) verifyLoginMFA(req *http.Request, vres auth.VerifyResult, username, totpCode string) mfaCheckOutcome {
	if vres.Session == nil || vres.Session.AccessToken == "" || r.auth.SupabaseClient() == nil {
		return mfaCheckOutcome{result: mfaNotRequired}
	}
	factorID, factorStatus, mfaErr := supabaseGetUserMFA(req, r.auth.SupabaseClient(), vres.Session.AccessToken)
	if mfaErr == nil {
		r.setUserSupabaseMFA(username, factorID != "" && factorStatus == "verified")
	}
	if mfaErr != nil {
		if r.userHasSupabaseMFA(username) {
			log.Printf("login: mfa lookup failed (%v) and the user DOES have MFA — denying (fail-closed)", mfaErr)
			return mfaCheckOutcome{result: mfaUnavailableDenied, required: true}
		}
		log.Printf("login: mfa lookup failed (%v) — no MFA for the user in the mirror, carrying on without MFA", mfaErr)
		return mfaCheckOutcome{result: mfaNotRequired}
	}
	if factorID == "" || factorStatus != "verified" {
		return mfaCheckOutcome{result: mfaNotRequired}
	}

	if ok, terr := r.trustedDeviceStoreFor(username).IsTrusted(readDeviceCookie(req)); terr != nil {
		log.Printf("login: trusted-device check error: %v — requiring 2FA", terr)
	} else if ok {
		return mfaCheckOutcome{result: mfaTrustedDevice, required: true, factorID: factorID}
	}

	if totpCode == "" {
		return mfaCheckOutcome{result: mfaCodeRequired, required: true, factorID: factorID}
	}
	ok, verifyErr := supabaseChallengeAndVerify(req, r.auth.SupabaseClient(), vres.Session.AccessToken, factorID, totpCode)
	if verifyErr != nil {
		log.Printf("login: supabase verify error %v — trying backup code", verifyErr)
	}
	if ok {
		return mfaCheckOutcome{result: mfaCodeVerified, required: true, factorID: factorID}
	}
	bcStore := r.backupCodeStoreFor(username)
	consumed, ccErr := bcStore.TryConsume(totpCode)
	if ccErr != nil {
		log.Printf("login: backup code load error: %v", ccErr)
	}
	if !consumed {
		return mfaCheckOutcome{result: mfaCodeInvalid, required: true, factorID: factorID}
	}
	return mfaCheckOutcome{result: mfaBackupUsed, required: true, factorID: factorID}
}

func (r *Router) handleLogin(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	ip := auth.ClientIP(req)
	if !r.limiter.Allow(ip) {
		writeErr(w, 429, "too many attempts, retry later")
		return
	}
	var body struct {
		Username       string `json:"username"`
		Password       string `json:"password"`
		TOTP           string `json:"totp,omitempty"`
		RememberDevice bool   `json:"remember_device,omitempty"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	body.Username = strings.TrimSpace(body.Username)
	if strings.Contains(body.Username, "@") {
		body.Username = strings.ToLower(body.Username)
		if um := r.auth.UUIDMap(); um != nil {
			if canonical, ok := um.LookupByEmail(body.Username); ok {
				body.Username = canonical
			}
		}
	}
	if r.userIsAppOnly(body.Username) {
		r.auditEvent(req, body.Username, "login.denied.app_only", "web panel blocked for an app-only account")
		writeErr(w, 401, "invalid credentials")
		return
	}
	if ok, until := r.lockout.Allowed(body.Username); !ok {
		retry := int(time.Until(until).Seconds())
		if retry < 1 {
			retry = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(retry))
		r.auditEvent(req, body.Username, "login.locked", "")
		writeErr(w, 423, "account temporarily locked after failed attempts, try again in "+strconv.Itoa(retry)+"s")
		return
	}
	vres, vErr := r.auth.VerifyDetailed(req.Context(), body.Username, body.Password)
	if !vres.OK {
		r.lockout.RecordFailure(body.Username)
		source := vres.Source
		if source == "" {
			source = "local"
		}
		detail := source
		if vErr != nil {
			detail += ":" + vErr.Error()
			if len(detail) > 200 {
				detail = detail[:200]
			}
		}
		r.auditEvent(req, body.Username, "login.fail", detail)
		writeErr(w, 401, "invalid credentials")
		return
	}
	loginSource := vres.Source
	if loginSource == "" {
		loginSource = "local"
	}

	mfa := r.verifyLoginMFA(req, vres, body.Username, body.TOTP)
	switch mfa.result {
	case mfaUnavailableDenied:
		r.lockout.RecordFailure(body.Username)
		r.auditEvent(req, body.Username, "login.mfa.unavailable_denied", "")
		writeErr(w, 503, "MFA verification is unavailable right now — try again in a moment")
		return
	case mfaCodeRequired:
		writeJSON(w, map[string]any{"totp_required": true, "source": "supabase"})
		return
	case mfaCodeInvalid:
		r.lockout.RecordFailure(body.Username)
		r.auditEvent(req, body.Username, "login.mfa.fail", "factor="+mfa.factorID)
		writeErr(w, 401, "invalid 2FA code")
		return
	case mfaTrustedDevice:
		loginSource = loginSource + "+mfa-trusted-device"
		r.auditEvent(req, body.Username, "login.mfa.trusted_device", "")
	case mfaBackupUsed:
		loginSource = loginSource + "+mfa-supabase"
		r.auditEvent(req, body.Username, "login.mfa.backup_used", "factor="+mfa.factorID)
	case mfaCodeVerified:
		loginSource = loginSource + "+mfa-supabase"
	}
	mfaRequired := mfa.required
	r.limiter.Reset(ip)
	r.lockout.RecordSuccess(body.Username)

	if mfaRequired && body.RememberDevice {
		if secret, mErr := r.trustedDeviceStoreFor(body.Username).Mint(
			body.Username, trimUA(req.Header.Get("User-Agent")), ip); mErr == nil {
			setDeviceCookie(w, secret)
			r.auditEvent(req, body.Username, "login.mfa.device_trusted", "")
		} else {
			log.Printf("login: trusted-device mint failed (%v) — login proceeds normally", mErr)
		}
	}

	tok, _, err := r.auth.Issue(body.Username, &auth.IssueMeta{
		IP:        ip,
		UserAgent: req.Header.Get("User-Agent"),
	})
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	r.auditEvent(req, body.Username, "login.ok", loginSource)
	if vres.Session != nil {
		if vres.Session.RefreshToken != "" {
			setSupabaseRefreshCookie(w, vres.Session.RefreshToken)
		}
		if vres.Session.AccessToken != "" {
			setSupabaseAccessCookie(w, vres.Session.AccessToken, vres.Session.ExpiresIn)
		}
	}
	if r.whatsappMgr != nil {
		if u, err := scope.New(body.Username); err == nil {
			r.whatsappMgr.WarmUp(u)
		}
	}
	setAuthCookie(w, tok)
	setCookieFlag(w, true)
	writeJSON(w, map[string]string{"token": tok, "user": body.Username})
}

func (r *Router) handleMe(w http.ResponseWriter, req *http.Request) {
	user := auth.UserFrom(req)
	resp := map[string]any{"user": user, "now": time.Now().Unix()}
	if um := r.auth.UUIDMap(); um != nil {
		if email := um.EmailFor(user); email != "" {
			resp["email"] = email
		}
	}
	writeJSON(w, resp)
}

func (r *Router) handleWSTicket(w http.ResponseWriter, req *http.Request) {
	user := auth.UserFrom(req)
	if user == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	jti := auth.JTIFrom(req)
	ticket := auth.IssueWSTicket(user, jti)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, map[string]any{
		"ticket":     ticket,
		"expires_in": int(auth.WSTicketTTL.Seconds()),
	})
}

func (r *Router) handleMobilePairStart(w http.ResponseWriter, req *http.Request) {
	user := auth.UserFrom(req)
	if user == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	r.cfgMu.Lock()
	publicHostname := r.cfg.PublicHostname
	r.cfgMu.Unlock()
	if publicHostname == "" {
		writeErr(w, 503, "server has no public hostname configured — QR pairing unavailable")
		return
	}
	ticket := auth.IssuePairingTicket(user)
	envelope := auth.PairingEnvelope{
		V:         auth.PairingEnvelopeVersion,
		Ticket:    ticket,
		ServerURL: "https://" + publicHostname,
		ExpiresIn: int(auth.PairingTicketTTL.Seconds()),
	}
	envelopeJSON, err := json.Marshal(envelope)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	png, err := qrcode.Encode(string(envelopeJSON), qrcode.Medium, 280)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	r.auditEvent(req, user, "mobile.pair.start", "")
	writeJSON(w, map[string]any{
		"v":          envelope.V,
		"ticket":     envelope.Ticket,
		"server_url": envelope.ServerURL,
		"expires_in": envelope.ExpiresIn,
		"qr_png":     base64.StdEncoding.EncodeToString(png),
	})
}

func (r *Router) handleRefresh(w http.ResponseWriter, req *http.Request) {
	user := auth.UserFrom(req)
	if user == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	tok, _, err := r.auth.Issue(user, &auth.IssueMeta{
		IP:        auth.ClientIP(req),
		UserAgent: req.Header.Get("User-Agent"),
	})
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if store := r.auth.Sessions(); store != nil {
		if oldJTI := auth.JTIFrom(req); oldJTI != "" {
			store.Revoke(oldJTI)
		}
	}
	setAuthCookie(w, tok)
	setCookieFlag(w, true)
	writeJSON(w, map[string]any{
		"token":      tok,
		"user":       user,
		"expires_in": int(auth.TokenTTL.Seconds()),
	})
}

func (r *Router) handleRefreshCookie(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	refresh := readSupabaseRefreshCookie(req)
	if refresh == "" {
		writeErr(w, 401, "no refresh session")
		return
	}
	sb := r.auth.SupabaseClient()
	if sb == nil {
		writeErr(w, 401, "refresh unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), 8*time.Second)
	defer cancel()
	sess, err := sb.RefreshSession(ctx, refresh)
	if err != nil {
		if errors.Is(err, auth.ErrSupabaseInvalidCredentials) {
			clearSupabaseRefreshCookie(w)
			clearSupabaseAccessCookie(w)
			writeErr(w, 401, "refresh expired")
			return
		}
		writeErr(w, 503, "refresh temporarily unavailable")
		return
	}
	username, ok := r.auth.UUIDMap().LookupByEmail(sess.Email)
	if !ok || username == "" {
		writeErr(w, 401, "unknown session")
		return
	}
	if r.userIsAppOnly(username) {
		r.auditEvent(req, username, "login.refresh.denied.app_only", "web panel blocked for an app-only account")
		writeErr(w, 401, "unknown session")
		return
	}
	tok, _, ierr := r.auth.Issue(username, &auth.IssueMeta{
		IP:        auth.ClientIP(req),
		UserAgent: req.Header.Get("User-Agent"),
	})
	if ierr != nil {
		writeErr(w, 500, ierr.Error())
		return
	}
	if sess.RefreshToken != "" {
		setSupabaseRefreshCookie(w, sess.RefreshToken)
	}
	if sess.AccessToken != "" {
		setSupabaseAccessCookie(w, sess.AccessToken, sess.ExpiresIn)
	}
	setAuthCookie(w, tok)
	setCookieFlag(w, true)
	r.auditEvent(req, username, "auth.refresh.cookie", "")
	writeJSON(w, map[string]any{
		"token":      tok,
		"user":       username,
		"expires_in": int(auth.TokenTTL.Seconds()),
	})
}

func (r *Router) handleLogout(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	user := auth.UserFrom(req)
	jti := auth.JTIFrom(req)
	if store := r.auth.Sessions(); store != nil && jti != "" {
		store.Revoke(jti)
	}
	logoutDetail := "local"
	if refresh := readSupabaseRefreshCookie(req); refresh != "" {
		if sb := r.auth.SupabaseClient(); sb != nil {
			ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
			if err := sb.RevokeRefreshToken(ctx, refresh); err != nil {
				log.Printf("logout: supabase revoke failed (best-effort): %v", err)
				logoutDetail = "supabase:revoke-failed"
			} else {
				logoutDetail = "supabase:revoked"
			}
			cancel()
		}
	}
	r.auditEvent(req, user, "logout", logoutDetail)
	clearAuthCookie(w)
	clearSupabaseRefreshCookie(w)
	clearSupabaseAccessCookie(w)
	setCookieFlag(w, false)
	writeJSON(w, map[string]any{"status": "ok"})
}

func (r *Router) handleSessionsList(w http.ResponseWriter, req *http.Request) {
	user := auth.UserFrom(req)
	store := r.auth.Sessions()
	if store == nil {
		writeJSON(w, []any{})
		return
	}
	currentJTI := auth.JTIFrom(req)
	out := store.ListForUser(user)
	wrapped := make([]map[string]any, 0, len(out))
	for _, s := range out {
		wrapped = append(wrapped, map[string]any{
			"jti":        s.JTI,
			"ip":         s.IP,
			"user_agent": s.UserAgent,
			"issued_at":  s.IssuedAt,
			"last_seen":  s.LastSeen,
			"expires_at": s.ExpiresAt,
			"current":    s.JTI == currentJTI,
		})
	}
	writeJSON(w, sanitizeList(wrapped, "jti"))
}

func (r *Router) handleSessionRevoke(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	user := auth.UserFrom(req)
	store := r.auth.Sessions()
	if store == nil {
		writeErr(w, 503, "sessions store unavailable")
		return
	}
	var body struct {
		JTI string `json:"jti"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	if body.JTI == "" {
		writeErr(w, 400, "jti required")
		return
	}
	owned := false
	for _, s := range store.ListForUser(user) {
		if s.JTI == body.JTI {
			owned = true
			break
		}
	}
	if !owned {
		writeErr(w, 404, "session not found")
		return
	}
	store.Revoke(body.JTI)
	r.auditEvent(req, user, "session.revoke", body.JTI[:min(8, len(body.JTI))])
	writeJSON(w, map[string]any{"status": "ok"})
}

func (r *Router) handleSessionRevokeAll(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	user := auth.UserFrom(req)
	store := r.auth.Sessions()
	if store == nil {
		writeErr(w, 503, "sessions store unavailable")
		return
	}
	currentJTI := auth.JTIFrom(req)
	n := store.RevokeAllExcept(user, currentJTI)
	r.auditEvent(req, user, "session.revoke_all", strconv.Itoa(n))
	writeJSON(w, map[string]any{"status": "ok", "revoked": n})
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (r *Router) handleListMobileSessions(w http.ResponseWriter, req *http.Request) {
	user := auth.UserFrom(req)
	if user == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	recs, err := r.credentialStore(user).ListAll()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(recs))
	for _, rec := range recs {
		out = append(out, map[string]any{
			"id":          rec.ID,
			"label":       rec.Label,
			"status":      rec.Status,
			"created_at":  rec.CreatedAt,
			"approved_at": rec.ApprovedAt,
		})
	}
	writeJSON(w, sanitizeList(out, "id"))
}

func (r *Router) handleApproveMobileCredential(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	user := auth.UserFrom(req)
	if user == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	if body.ID == "" {
		writeErr(w, 400, "id required")
		return
	}
	store := r.credentialStore(user)
	if _, found, err := store.CredentialByID(body.ID); err != nil {
		writeErr(w, 500, err.Error())
		return
	} else if !found {
		writeErr(w, 404, "credential not found")
		return
	}
	if err := store.Approve(body.ID); err != nil {
		writeErr(w, 404, "credential not found")
		return
	}
	r.auditEvent(req, user, "mobile.credential.approve", body.ID[:min(8, len(body.ID))])
	writeJSON(w, map[string]any{"status": "ok"})
}

func (r *Router) handleRevokeMobileSession(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	user := auth.UserFrom(req)
	if user == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	if body.ID == "" {
		writeErr(w, 400, "id required")
		return
	}
	store := r.credentialStore(user)
	if _, found, err := store.CredentialByID(body.ID); err != nil {
		writeErr(w, 500, err.Error())
		return
	} else if !found {
		writeErr(w, 404, "credential not found")
		return
	}
	if err := store.Remove(body.ID); err != nil {
		writeErr(w, 404, "credential not found")
		return
	}
	r.auditEvent(req, user, "mobile.credential.revoke", body.ID[:min(8, len(body.ID))])
	writeJSON(w, map[string]any{"status": "ok"})
}

func (r *Router) handleDenyMobileCredential(w http.ResponseWriter, req *http.Request) {
	r.handleRevokeMobileSession(w, req)
}

func (r *Router) totpSecretFor(username string) (string, bool) {
	r.cfgMu.Lock()
	defer r.cfgMu.Unlock()
	return r.cfg.TOTPSecretFor(username)
}

const totpIssuer = "Server Control Panel"

func (r *Router) handleTOTPStatus(w http.ResponseWriter, req *http.Request) {
	user := auth.UserFrom(req)
	if user == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	_, enrolled := r.totpSecretFor(user)
	writeJSON(w, map[string]any{"enrolled": enrolled, "user": user})
}

func (r *Router) handleTOTPEnroll(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	user := auth.UserFrom(req)
	if user == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	if _, already := r.totpSecretFor(user); already {
		writeErr(w, 409, "2FA is already enabled; disable it before generating a new secret")
		return
	}
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      totpIssuer,
		AccountName: user,
	})
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	png, err := qrcode.Encode(key.URL(), qrcode.Medium, 256)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
	writeJSON(w, map[string]any{
		"secret":  key.Secret(),
		"otpauth": key.URL(),
		"qr":      dataURL,
		"issuer":  totpIssuer,
		"account": user,
	})
}

func (r *Router) handleTOTPConfirm(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	user := auth.UserFrom(req)
	if user == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	var body struct {
		Secret string `json:"secret"`
		Code   string `json:"code"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	if body.Secret == "" || body.Code == "" {
		writeErr(w, 400, "secret and code are required")
		return
	}
	if !totp.Validate(body.Code, body.Secret) {
		r.auditEvent(req, user, "totp.enroll.fail", "")
		writeErr(w, 401, "invalid code")
		return
	}
	r.cfgMu.Lock()
	updated := r.cfg.SetTOTPSecret(user, body.Secret)
	var err error
	if updated {
		err = config.Save(r.cfg, filepath.Join(r.cfg.DataDir, "config.json"))
	}
	r.cfgMu.Unlock()
	if !updated {
		writeErr(w, 500, "user not found")
		return
	}
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	r.auditEvent(req, user, "totp.enroll.ok", "")
	writeJSON(w, map[string]any{"status": "ok"})
}

func (r *Router) handleTOTPDisable(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	user := auth.UserFrom(req)
	if user == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	secret, enrolled := r.totpSecretFor(user)
	if !enrolled {
		writeJSON(w, map[string]any{"status": "ok", "note": "was already disabled"})
		return
	}
	if !totp.Validate(body.Code, secret) {
		r.auditEvent(req, user, "totp.disable.fail", "")
		writeErr(w, 401, "invalid code")
		return
	}
	r.cfgMu.Lock()
	r.cfg.SetTOTPSecret(user, "")
	err := config.Save(r.cfg, filepath.Join(r.cfg.DataDir, "config.json"))
	r.cfgMu.Unlock()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	r.auditEvent(req, user, "totp.disable.ok", "")
	writeJSON(w, map[string]any{"status": "ok"})
}

func (r *Router) handleChangePassword(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	user := auth.UserFrom(req)
	if user == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	var body struct {
		Old string `json:"old"`
		New string `json:"new"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	if !r.auth.Verify(user, body.Old) {
		r.auditEvent(req, user, "password.change.fail", "old_password_invalid")
		writeErr(w, 401, "current password is wrong")
		return
	}
	if len(body.New) < 8 {
		writeErr(w, 400, "new password must be 8+ characters")
		return
	}

	supabaseUpdated := false
	if r.auth.SupabaseClient() != nil && readSupabaseAccessCookie(req) != "" {
		status, err := r.supabaseCall(w, req, "PUT", "/auth/v1/user",
			map[string]string{"password": body.New}, nil)
		if err != nil {
			r.auditEvent(req, user, "password.change.fail",
				fmt.Sprintf("supabase err=%s", err.Error()))
			writeErr(w, 502, "supabase unavailable: "+err.Error())
			return
		}
		if status != http.StatusOK {
			r.auditEvent(req, user, "password.change.fail",
				fmt.Sprintf("supabase status=%d", status))
			writeErr(w, status, "supabase rejected the password (status "+strconv.Itoa(status)+")")
			return
		}
		supabaseUpdated = true
	}

	hash, err := auth.HashPassword(body.New)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	r.cfgMu.Lock()
	updated := r.cfg.SetPassword(user, hash)
	if updated {
		err = config.Save(r.cfg, filepath.Join(r.cfg.DataDir, "config.json"))
	}
	r.cfgMu.Unlock()
	if !updated {
		writeErr(w, 500, "user not found")
		return
	}
	if err != nil {
		log.Printf("password.change: WARN local save failed after supabase OK: %v", err)
		writeErr(w, 500, "local save: "+err.Error())
		return
	}
	r.auth = auth.New(r.cfg.JWTSecret, credsFromConfig(r.cfg))
	if r.cfg.SupabaseURL != "" && r.cfg.SupabaseAnonKey != "" {
		backend := auth.BackendBoth
		if v := os.Getenv("PANEL_AUTH_BACKEND"); v != "" {
			backend = auth.AuthBackend(v)
		} else if r.cfg.AuthBackend != "" {
			backend = auth.AuthBackend(r.cfg.AuthBackend)
		}
		uuidMap, mapErr := auth.LoadUUIDMap(filepath.Join(r.cfg.DataDir, "migration-uuid-map.json"))
		if mapErr == nil && uuidMap != nil && uuidMap.Size() > 0 {
			sbClient := auth.NewSupabaseClient(r.cfg.SupabaseURL, r.cfg.SupabaseAnonKey)
			r.auth = r.auth.WithSupabase(backend, sbClient, uuidMap)
		}
	}
	target := "local-only"
	if supabaseUpdated {
		target = "supabase+local"
	}
	r.auditEvent(req, user, "password.change", target)
	if err := r.trustedDeviceStoreFor(user).RevokeAll(); err != nil {
		log.Printf("password.change: revoke trusted devices failed: %v", err)
	} else {
		r.auditEvent(req, user, "password.change", "devices_revoked")
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (r *Router) handleSetupEnroll(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	var body struct {
		SetupToken string `json:"setup_token"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	username, err := r.auth.VerifySetupToken(body.SetupToken)
	if err != nil {
		writeErr(w, 401, "setup token invalid or expired — log in again")
		return
	}

	mainKey, err := totp.Generate(totp.GenerateOpts{
		Issuer:      totpIssuer,
		AccountName: username,
	})
	if err != nil {
		writeErr(w, 500, "totp generate: "+err.Error())
		return
	}
	recoveryKey, err := totp.Generate(totp.GenerateOpts{
		Issuer:      totpIssuer + " (recovery)",
		AccountName: username,
	})
	if err != nil {
		writeErr(w, 500, "totp generate (recovery): "+err.Error())
		return
	}
	mainPNG, err := qrcode.Encode(mainKey.URL(), qrcode.Medium, 256)
	if err != nil {
		writeErr(w, 500, "qr encode: "+err.Error())
		return
	}
	recoveryPNG, err := qrcode.Encode(recoveryKey.URL(), qrcode.Medium, 256)
	if err != nil {
		writeErr(w, 500, "qr encode (recovery): "+err.Error())
		return
	}
	writeJSON(w, map[string]any{
		"main_secret":      mainKey.Secret(),
		"main_otpauth":     mainKey.URL(),
		"main_qr":          "data:image/png;base64," + base64.StdEncoding.EncodeToString(mainPNG),
		"recovery_secret":  recoveryKey.Secret(),
		"recovery_otpauth": recoveryKey.URL(),
		"recovery_qr":      "data:image/png;base64," + base64.StdEncoding.EncodeToString(recoveryPNG),
		"issuer":           totpIssuer,
		"account":          username,
	})
}

func (r *Router) handleSetupConfirm(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	var body struct {
		SetupToken     string `json:"setup_token"`
		MainSecret     string `json:"main_secret"`
		MainCode       string `json:"main_code"`
		RecoverySecret string `json:"recovery_secret"`
		RecoveryCode   string `json:"recovery_code"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	username, err := r.auth.VerifySetupToken(body.SetupToken)
	if err != nil {
		writeErr(w, 401, "setup token invalid or expired — log in again")
		return
	}
	if body.MainSecret == "" || body.MainCode == "" || body.RecoverySecret == "" || body.RecoveryCode == "" {
		writeErr(w, 400, "all fields are required")
		return
	}
	if !totp.Validate(body.MainCode, body.MainSecret) {
		r.auditEvent(req, username, "setup.totp.main.fail", "")
		writeErr(w, 401, "invalid primary TOTP code")
		return
	}
	if !totp.Validate(body.RecoveryCode, body.RecoverySecret) {
		r.auditEvent(req, username, "setup.totp.recovery.fail", "")
		writeErr(w, 401, "invalid recovery TOTP code")
		return
	}
	r.cfgMu.Lock()
	okMain := r.cfg.SetTOTPSecret(username, body.MainSecret)
	okRecovery := r.cfg.SetRecoveryTOTPSecret(username, body.RecoverySecret)
	var saveErr error
	if okMain && okRecovery {
		saveErr = config.Save(r.cfg, filepath.Join(r.cfg.DataDir, "config.json"))
	}
	r.cfgMu.Unlock()
	if !okMain || !okRecovery {
		writeErr(w, 500, "user not found")
		return
	}
	if saveErr != nil {
		writeErr(w, 500, "save: "+saveErr.Error())
		return
	}
	ip := auth.ClientIP(req)
	tok, _, err := r.auth.Issue(username, &auth.IssueMeta{
		IP:        ip,
		UserAgent: req.Header.Get("User-Agent"),
	})
	if err != nil {
		writeErr(w, 500, "issue: "+err.Error())
		return
	}
	r.auditEvent(req, username, "setup.totp.enrolled", "main+recovery")
	writeJSON(w, map[string]any{"token": tok, "username": username})
}
