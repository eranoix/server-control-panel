package api

// handlers_auth.go — authentication, sessions and 2FA (TOTP).
//
// Split out of api.go to cut down the mass of the central file. These stay
// methods on *Router because they depend on cfg/cfgMu, auth, audit,
// limiter, lockout, sessions, supabase.
//
// Covers:
//   - login / me / refresh / logout
//   - listing and revoking sessions
//   - TOTP enroll/confirm/disable (legacy; the new MFA goes via Supabase)
//   - password change (dual-write Supabase+local)
//   - first-login setup (setup-enroll + setup-confirm)

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

// trimUA shortens the User-Agent into a short UI label (the trusted-device
// list). Display only — it is not used in any security decision.
func trimUA(ua string) string {
	ua = strings.TrimSpace(ua)
	const max = 120
	if len(ua) > max {
		return ua[:max] + "…"
	}
	return ua
}

// mfaCheckResult is verifyLoginMFA's verdict — the caller decides the HTTP
// response and the audit event from it; verifyLoginMFA never writes to the
// response and never records an audit entry itself (extracted from handleLogin;
// see handlers_auth_test.go for the characterisation suite that proves
// byte-for-byte that this extraction changed none of the branches below).
type mfaCheckResult int

const (
	// mfaNotRequired: user with no Supabase factor enrolled (or the lookup
	// failed and the local mirror gave no sign of MFA) — login proceeds without
	// demanding a second factor.
	mfaNotRequired mfaCheckResult = iota
	// mfaUnavailableDenied: the user DOES have MFA in the local mirror but the
	// Supabase lookup failed — fail closed, never let anyone through without the
	// second factor.
	mfaUnavailableDenied
	// mfaTrustedDevice: factor enrolled, but the device is already trusted (a
	// valid trust cookie) — second factor waived.
	mfaTrustedDevice
	// mfaCodeRequired: factor enrolled, no trusted device, no code supplied —
	// the caller should answer {"totp_required": true}.
	mfaCodeRequired
	// mfaCodeVerified: Supabase TOTP code validated successfully.
	mfaCodeVerified
	// mfaBackupUsed: the Supabase TOTP code was invalid, but a valid backup
	// code covered the second factor (consumed one-shot).
	mfaBackupUsed
	// mfaCodeInvalid: the Supabase TOTP code was invalid AND no backup code
	// matched — the caller must deny the login (401).
	mfaCodeInvalid
)

// mfaCheckOutcome bundles the verdict with the little bit of state the caller
// needs to build the audit entry or the error (factorID) and to drive the
// "remember this device" block that follows (required).
type mfaCheckOutcome struct {
	result   mfaCheckResult
	required bool // true sse havia factor enrolado — usado pelo bloco RememberDevice
	factorID string
}

// verifyLoginMFA concentrates the second-factor policy of password login:
// Supabase factor lookup, the skip for a trusted device, the TOTP
// challenge/verify and the one-shot backup-code fallback. Extracted from
// handleLogin to isolate the MFA decision from the HTTP orchestration — the
// observable behaviour is identical to the previous inline version, pinned by
// handlers_auth_test.go BEFORE this extraction existed.
//
// As a side effect it syncs the local MFA mirror (setUserSupabaseMFA) whenever
// the lookup succeeded — the same side effect the inline code already had,
// preserved here because handleMe and other screens read that mirror.
func (r *Router) verifyLoginMFA(req *http.Request, vres auth.VerifyResult, username, totpCode string) mfaCheckOutcome {
	if vres.Session == nil || vres.Session.AccessToken == "" || r.auth.SupabaseClient() == nil {
		return mfaCheckOutcome{result: mfaNotRequired}
	}
	factorID, factorStatus, mfaErr := supabaseGetUserMFA(req, r.auth.SupabaseClient(), vres.Session.AccessToken)
	// Sync the local mirror for the admin UI: factor verified → true, absent → false.
	if mfaErr == nil {
		r.setUserSupabaseMFA(username, factorID != "" && factorStatus == "verified")
	}
	if mfaErr != nil {
		// FAIL CLOSED: if the user is known to be enrolled (local mirror), we do
		// not let them through without the second factor just because Supabase
		// failed — that would be an MFA bypass by unavailability. With no MFA
		// mirror, the door stays open (a user without 2FA is not punished for a
		// blip).
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

	// Device already trusted? Skip the code (the password was validated above).
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
	// Accept an email in the username field (the UX mirrors the web app). If the
	// input contains `@`, resolve it through uuid_map.LookupByEmail → canonical
	// username. The internal identity ("sam") stays intact — data/users/sam
	// paths, audit, TOTP keys, all preserved.
	body.Username = strings.TrimSpace(body.Username)
	if strings.Contains(body.Username, "@") {
		// Email: Supabase stores it lowercase; normalising here keeps the JWT and
		// the audit log from carrying the form's original casing (inconsistent
		// with what Supabase uses in downstream queries).
		body.Username = strings.ToLower(body.Username)
		if um := r.auth.UUIDMap(); um != nil {
			if canonical, ok := um.LookupByEmail(body.Username); ok {
				body.Username = canonical
			}
			// If it does not match, leave the input as it came — Verify will
			// reject it with invalid_credentials, and the audit log keeps the email
			// that was tried (useful to tell a typo from an attack).
		}
	}
	// APP-ONLY GATE: accounts marked AppOnly sign in through the Android app
	// (MobileLogin/passkey) and are refused HERE, before any credential check —
	// no password travels to Supabase, no GoTrue session is created, no cookie
	// is issued for an attempt that is already decided.
	//
	// The answer is the SAME as a wrong password (401 "invalid credentials"),
	// word for word: a specific message ("this account is app-only") would tell
	// an attacker that the user exists — free account enumeration. The real
	// reason goes only to the audit log, which is internal.
	//
	// It comes after the email→canonical-username normalisation just above,
	// otherwise the same account would slip past the gate merely by logging in
	// with its email.
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
		writeErr(w, 423, "conta temporariamente bloqueada por falhas, tente em "+strconv.Itoa(retry)+"s")
		return
	}
	// VerifyDetailed runs the backend policy (local | supabase | both) and
	// reports which source approved, so the audit log can tell them apart. Under
	// BackendBoth, a Supabase network failure falls back to local bcrypt
	// transparently — a WARN at boot has already told the operator.
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

	// MFA goes exclusively through Supabase — see verifyLoginMFA.
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

	// The user asked to trust this device. That only makes sense when there was
	// a second factor (mfaRequired) — with no MFA there is no code to skip. Mint
	// a fresh opaque secret and store it in the cookie. A mint failure is
	// NON-FATAL: the login must not fail because of it.
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
	// Capture the Supabase refresh_token (only when Source=supabase and a
	// session came back). An httpOnly cookie, path-restricted to the /api/auth/
	// endpoint — it only travels on refresh/logout, never on the other APIs.
	//
	// Retrofitted later: it also captures the access_token. The MFA endpoints
	// read it from that cookie as a Bearer; when it expires (about an hour),
	// supabaseCallWithRefresh uses the refresh above to rotate it transparently.
	if vres.Session != nil {
		if vres.Session.RefreshToken != "" {
			setSupabaseRefreshCookie(w, vres.Session.RefreshToken)
		}
		if vres.Session.AccessToken != "" {
			setSupabaseAccessCookie(w, vres.Session.AccessToken, vres.Session.ExpiresIn)
		}
	}
	// WhatsApp warm-up (v2): fires systemctl start vpsm-whatsapp@<user> in the
	// background. Idempotent (a no-op if it is already running). No await — the
	// UI shows STARTING via /api/whatsapp/status until the container answers.
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
	// Expose the mapped email so the UI can show the Supabase identity.
	if um := r.auth.UUIDMap(); um != nil {
		if email := um.EmailFor(user); email != "" {
			resp["email"] = email
		}
	}
	writeJSON(w, resp)
}

// handleWSTicket issues a one-shot WS ticket for the authenticated user. It
// lets the frontend open a WebSocket without putting the JWT in the query
// string (?token=) — a vector that leaks into access logs, the Referer header
// and browser history.
//
// Flow:
//  1. The frontend calls GET /api/auth/ws-ticket (valid HttpOnly cookie)
//  2. The backend returns {"ticket": "<60s one-shot>"}
//  3. The frontend opens the WS with ?ticket=<X>
//  4. The backend consumes the ticket and authenticates (extractWSAuth)
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

// handleMobilePairStart issues a QR-code pairing ticket for the user ALREADY
// AUTHENTICATED in the desktop/web dashboard. The ticket itself is NOT a
// credential: it only authorises, once, an exchange for a webauthn_reg token
// (through the public POST /api/mobile/v1/auth/pair on the BFF) — which in turn
// only authorises starting a passkey registration ceremony, and never issues a
// session (see the docstring of FinishPasskeyRegistration in passkey.go: every
// credential is born pending and needs explicit approval in the dashboard).
//
// The username ALWAYS comes from auth.UserFrom(req) — never from the request
// body — so that minting a pairing ticket for another user is impossible.
//
// The QR code does not carry just the ticket: it carries the versioned envelope
// auth.PairingEnvelope, which includes the destination server
// (Config.PublicHostname). That spares the user from typing the server address
// on the new phone (ServerSetupScreen becomes merely the fallback path) — but
// the app decides on its own side whether it accepts being repointed at that
// server (ServerConfigRepository.configure, which refuses a silent repoint).
// With no PublicHostname configured, QR pairing is unavailable: minting an
// envelope with an empty server_url would produce a QR the app cannot use, so
// failing explicitly here is better than a mute QR.
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

// handleRefresh issues a fresh JWT to the currently-authenticated user and
// rotates the session id — a leaked old token only works until the next
// refresh, which is a meaningful security property.
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
	// Revoke the previous session id so the rotated-out JWT can't be reused.
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

// handleRefreshCookie issues a fresh JWT WITHOUT depending on a still-valid
// access token in the header — it uses only the HttpOnly `vpsm_refresh` cookie
// (the GoTrue refresh_token, valid about 30 days) to rotate the Supabase
// session and mint a fresh vps-manager JWT.
//
// Why it exists, separate from the protected handleRefresh: when the access
// token expires (idle tab, backgrounded, laptop suspended), handleRefresh —
// which runs behind the Middleware and requires a valid JWT — answers 401 and
// there is no way to renew silently. The frontend falls through HERE. This is
// what makes the terminal (and the app) "unbreakable": the session restores
// itself for as long as the refresh_token lives, instead of dropping the user
// onto the login screen.
//
// A PUBLIC route (outside the protected mux) on purpose — it cannot require a
// valid JWT. CSRF: the cookie is SameSite=Lax + Path=/api/auth/, so a
// cross-site POST does not carry it; and the response is opaque cross-origin
// (CORS). Revocation is still GoTrue's (logout calls RevokeRefreshToken).
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
		// invalid_grant → the refresh_token has expired or been revoked for good.
		// Clear the cookie so the frontend stops trying and falls back to
		// re-login (no loop).
		if errors.Is(err, auth.ErrSupabaseInvalidCredentials) {
			clearSupabaseRefreshCookie(w)
			clearSupabaseAccessCookie(w)
			writeErr(w, 401, "refresh expired")
			return
		}
		// network/5xx — transient. 503 so the frontend retries without dropping the session.
		writeErr(w, 503, "refresh temporarily unavailable")
		return
	}
	// Remap the Supabase identity to the canonical vps-manager username.
	username, ok := r.auth.UUIDMap().LookupByEmail(sess.Email)
	if !ok || username == "" {
		writeErr(w, 401, "unknown session")
		return
	}
	// App-only gate (the same policy as handleLogin): this endpoint mints a
	// DASHBOARD JWT from the Supabase cookie. Today that cookie is only born in
	// handleLogin — which already blocks the account — but the gate applies here
	// too, so as not to depend on that assumption: any future path that lets a
	// Supabase refresh reach this point would still be closed. Same generic
	// message as above, real reason in the audit log.
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
	// Rotate the Supabase cookies (GoTrue rotates the refresh_token on every use)
	// and store the new vps-manager JWT.
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

// ---------- Sessions ----------

// handleLogout revokes the current session id so the JWT can't be reused.
// The browser is expected to also drop the token from localStorage, but
// this endpoint is what makes "logged out" mean it server-side.
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
	// Revoke the refresh_token on GoTrue (best effort; it does not block the
	// local logout). Reads the httpOnly vpsm_refresh cookie set at login.
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

// handleSessionsList returns the user's active sessions, newest first. The
// caller's current session is flagged with `current: true` so the UI can
// hide the revoke button on it (and show "esta sessão").
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
	// The frontend iterates with :key="s.jti"; a session with no persisted JTI
	// (legacy format) used to crash the whole dashboard until this sanitisation.
	writeJSON(w, sanitizeList(wrapped, "jti"))
}

// handleSessionRevoke kills a single session by jti. Caller must own it
// (we only let users revoke their own sessions).
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
	// Confirm ownership: scan user's sessions for the jti.
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

// handleSessionRevokeAll terminates every session for the calling user
// EXCEPT the one making the request (so the user doesn't lock themselves out).
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

// ---------- Paired devices (passkeys) ----------
//
// This is the approval side that closes QR-code pairing (see the top docstring
// of internal/auth/webauthn_credentials.go): a credential is born pending and
// only becomes login-capable through Approve(), called here from an already
// authenticated desktop session. Without this file no passkey could ever log
// in — Approve() is called from no other code path in the project.
//
// Isolation by construction: the four functions below NEVER open a
// WebAuthnCredentialsStore by global ID — always through
// r.credentialStore(auth.UserFrom(req)), which resolves to the per-user file
// (WebAuthnCredentialsPath). Another user's ID simply does not exist inside the
// caller's file, so every operation below answers 404 (not 403) without ever
// having to compare an "owner" — there is no other owner within reach.

// handleListMobileSessions lists the calling user's passkey credentials, both
// pending and approved — the material the person approving needs in order to
// decide for real (device label, when pairing was requested, when it was
// approved), not a bare "approve?" with no context.
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

// handleApproveMobileCredential is the ONLY code path in the whole project that
// makes a pending credential login-capable (it calls
// WebAuthnCredentialsStore.Approve). It is only reachable authenticated on this
// protected desktop-dashboard route — never through the pairing ticket (which
// only authorises REGISTERING; see handleMobilePairStart) and never by the
// device being approved (the app never sees a desktop session JWT before
// approval — FinishPasskeyRegistration never issues one).
//
// Approving twice is idempotent: Approve() merely rewrites the same
// status/ApprovedAt, with no error.
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
	// list-then-match: confirm the ID exists in THIS user's file before
	// approving — never reveal whether the ID belongs to another user
	// (always 404, never 403).
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

// handleRevokeMobileSession removes a credential — pending (denying the
// pairing) or approved (revoking the access). Remove() does not look at status,
// which is why handleDenyMobileCredential reuses this same function: denying a
// pending pairing and revoking an approved passkey are the SAME operation in
// the store.
//
// Revoking makes the credential immediately unusable for login: it disappears
// from the file, and FinishPasskeyLogin (internal/api/passkey.go) resolves the
// status through CredentialByID AFTER the cryptographic verification — with no
// record it lands on ErrPasskeyInvalidCredential, never on "approved". Revoking
// the credential the caller is using RIGHT NOW does not drop the current
// desktop session: sessions (internal/sessions.Store, keyed by JWT/jti) and
// passkey credentials (per user, in this file) are completely separate
// stores.
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

// handleDenyMobileCredential denies a pairing that is still pending. It reuses
// handleRevokeMobileSession: Remove() is the same operation for denying
// (pending) and revoking (approved) — see the docstring above.
func (r *Router) handleDenyMobileCredential(w http.ResponseWriter, req *http.Request) {
	r.handleRevokeMobileSession(w, req)
}

// ---------- 2FA TOTP ----------

// totpSecretFor proxies through to the Config under the cfg mutex so concurrent
// password changes (which rewrite the file) can't race the lookup.
func (r *Router) totpSecretFor(username string) (string, bool) {
	r.cfgMu.Lock()
	defer r.cfgMu.Unlock()
	return r.cfg.TOTPSecretFor(username)
}

const totpIssuer = "VPS Manager"

func (r *Router) handleTOTPStatus(w http.ResponseWriter, req *http.Request) {
	user := auth.UserFrom(req)
	if user == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	_, enrolled := r.totpSecretFor(user)
	writeJSON(w, map[string]any{"enrolled": enrolled, "user": user})
}

// handleTOTPEnroll generates a fresh TOTP secret + QR for the current user.
// The secret is NOT persisted yet — only after the user confirms with a valid
// code via handleTOTPConfirm. This avoids locking the user out if they bail
// halfway through enrollment.
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
	// Encode the PNG as a data URL so the browser can render it without a
	// follow-up request that would need to carry the (pre-confirm) secret.
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
	writeJSON(w, map[string]any{
		"secret":  key.Secret(),
		"otpauth": key.URL(),
		"qr":      dataURL,
		"issuer":  totpIssuer,
		"account": user,
	})
}

// handleTOTPConfirm verifies the user typed a code that matches the candidate
// secret and only then persists it.
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

// handleTOTPDisable requires the user to type a current valid TOTP code to
// disable 2FA — prevents a stolen session from removing it.
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

	// A password change is now a transactional DUAL write.
	// Supabase is the source of truth (login validation prefers it); local
	// bcrypt remains the fallback while the two coexist (it will be removed
	// once the migration is complete).
	//
	// The order matters:
	//   1. Update Supabase via PUT /auth/v1/user — on failure, reject it ALL.
	//   2. Only then update the local bcrypt hash in config.json.
	//
	// If step 1 fails, nothing changes — the user can try again. If step 2
	// fails after step 1 succeeded, Supabase holds the new password while the
	// local copy keeps the old one — the fallback would be out of step in Both
	// mode. Accepted because step 2 rarely fails (a local disk write) and the
	// user goes on logging in through Supabase.
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
		// Supabase has already been updated — there is no graceful rollback.
		// Log and carry on; the next restart re-reads from disk (the atomic Save
		// may have failed mid-flight, but usually the fsync already landed).
		log.Printf("password.change: WARN local save failed after supabase OK: %v", err)
		writeErr(w, 500, "local save: "+err.Error())
		return
	}
	// replace auth service secret-carrying state with refreshed credentials
	r.auth = auth.New(r.cfg.JWTSecret, credsFromConfig(r.cfg))
	// Re-wire the Supabase backend (auth.New zeroed it). Same constructor as at boot.
	if r.cfg.SupabaseURL != "" && r.cfg.SupabaseAnonKey != "" {
		backend := auth.BackendBoth
		if v := os.Getenv("VPSM_AUTH_BACKEND"); v != "" {
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
	// A password change revokes every trusted device — a compromised password
	// must not leave 2FA trusts standing. Best effort.
	if err := r.trustedDeviceStoreFor(user).RevokeAll(); err != nil {
		log.Printf("password.change: revoke trusted devices failed: %v", err)
	} else {
		r.auditEvent(req, user, "password.change", "devices_revoked")
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

// ---------- First-login TOTP setup (mandatory 2FA enrollment) ----------

// handleSetupEnroll generates a pair of fresh TOTP secrets (primary + recovery)
// and returns them with QR codes for the user to scan. NO state is persisted
// here — the secrets only get saved once /api/auth/setup-confirm validates a
// code for each. Authorized via setup_token (5min, single-purpose) emitted by
// /api/auth/login when 2FA is missing.
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

// handleSetupConfirm validates the codes the user typed (one per secret),
// persists BOTH secrets atomically, and emits a real JWT session.
// The flow is transactional: if either code is wrong, NOTHING is saved.
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
	// Both codes valid — persist atomically under the config lock.
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
	// Mint the real session JWT.
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
