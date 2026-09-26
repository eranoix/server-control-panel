// mfa.go — MFA TOTP endpoints via self-hosted GoTrue.
//
// 4 handlers for enroll/verify/disable/status. All protected by
// auth.Middleware (JWT v2). Every user-scoped GoTrue call goes through the
// helper auth.SupabaseClient.AuthenticatedRequest, which refreshes
// transparently when the access_token has expired.
//
// Endpoints:
//
//	POST /api/auth/mfa/enroll-start  — creates an unverified TOTP factor. Returns
//	                                    qr_code (an encoded totpauth uri) +
//	                                    factor_id + secret for the frontend to show.
//	POST /api/auth/mfa/enroll-verify — takes {factor_id, code}. Challenge+verify
//	                                    on GoTrue; on success it mints 10 backup codes
//	                                    and returns them ONCE.
//	POST /api/auth/mfa/disable       — DELETE factor + erase the backup-codes file.
//	GET  /api/auth/mfa/status        — {enrolled, factor_id, backup_codes_unused}.
//
// IMPORTANT: backup codes are shown ONLY at enroll-verify. Regenerating them
// requires a full disable + re-enroll. Idempotency: enroll-start called
// twice creates two unverified factors — the backend does not dedup; the
// frontend has to keep the flow linear (one enrolment at a time).
package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"server-control-panel/internal/auth"
)

// mfaFactor is the subset of auth.mfa_factors / GoTrue's response we care about.
type mfaFactor struct {
	ID           string `json:"id"`
	FriendlyName string `json:"friendly_name,omitempty"`
	Type         string `json:"factor_type"`
	Status       string `json:"status"`
}

type gotrueUserResp struct {
	ID      string      `json:"id"`
	Email   string      `json:"email"`
	Factors []mfaFactor `json:"factors,omitempty"`
}

// supabaseCall wraps AuthenticatedRequest + propagates the updated cookie when
// a refresh happened. Returns the parsed body in destPtr (when non-nil) + status.
//
// Structured logs on EVERY call: method+path+status+body_size+parse_status.
// The failure path includes a sample of the body for quick troubleshooting.
func (r *Router) supabaseCall(w http.ResponseWriter, req *http.Request, method, path string, body any, destPtr any) (int, error) {
	sb := r.auth.SupabaseClient()
	if sb == nil {
		return 0, fmt.Errorf("supabase client not configured")
	}
	access := readSupabaseAccessCookie(req)
	refresh := readSupabaseRefreshCookie(req)
	if access == "" && refresh == "" {
		return 0, fmt.Errorf("no supabase session cookies — relogin")
	}
	res, err := sb.AuthenticatedRequest(req.Context(), access, refresh, method, path, body)
	if err != nil {
		log.Printf("supabaseCall: %s %s network_err=%v", method, path, err)
		return 0, err
	}
	// The verbose status was removed (it logged EVERY call — noise in production).
	// Errors and non-2xx show up at the call sites through auditEvent + writeErr.
	if res.Refreshed != nil {
		setSupabaseAccessCookie(w, res.Refreshed.AccessToken, res.Refreshed.ExpiresIn)
		if res.Refreshed.RefreshToken != "" {
			setSupabaseRefreshCookie(w, res.Refreshed.RefreshToken)
		}
	}
	if destPtr != nil && len(res.Body) > 0 {
		if err := json.Unmarshal(res.Body, destPtr); err != nil {
			log.Printf("supabaseCall: %s %s parse_err=%v body_first_500=%s body_last_200=%s",
				method, path, err, truncate(string(res.Body), 500), tailString(string(res.Body), 200))
			return res.StatusCode, fmt.Errorf("parse %w (body_size=%d)", err, len(res.Body))
		}
	}
	return res.StatusCode, nil
}

// tailString returns the last n chars of s. Handy for seeing where JSON broke.
func tailString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// findFactorByType returns the factor_id of the type being looked for (e.g. "totp") OR empty.
func findFactorByType(user *gotrueUserResp, factorType string) (id, status string) {
	if user == nil {
		return "", ""
	}
	for _, f := range user.Factors {
		if f.Type == factorType {
			return f.ID, f.Status
		}
	}
	return "", ""
}

// ---------------- POST /api/auth/mfa/enroll-start ----------------

func (r *Router) handleMFAEnrollStart(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	username := auth.UserFrom(req)
	if username == "" {
		writeErr(w, 401, "unauthorized")
		return
	}

	// Optional body: friendly_name (default "vpsmanager-v2").
	type enrollStartReq struct {
		FriendlyName string `json:"friendly_name,omitempty"`
	}
	var body enrollStartReq
	_ = json.NewDecoder(req.Body).Decode(&body)
	if strings.TrimSpace(body.FriendlyName) == "" {
		body.FriendlyName = "vpsmanager-v2"
	}

	// IDEMPOTENCY: GoTrue rejects a second unverified factor with the same
	// friendly_name → 422. Before creating a new one, delete any existing
	// unverified TOTP factor. Verified factors are preserved (Disable is a
	// separate, explicit path).
	var currentUser gotrueUserResp
	if userStatus, userErr := r.supabaseCall(w, req, "GET", "/auth/v1/user", nil, &currentUser); userErr == nil && userStatus == http.StatusOK {
		for _, f := range currentUser.Factors {
			if f.Type == "totp" && f.Status == "unverified" {
				_, _ = r.supabaseCall(w, req, "DELETE", "/auth/v1/factors/"+f.ID, nil, nil)
				log.Printf("mfa.enroll_start: user=%s cleaned stale unverified factor=%s", username, f.ID)
			}
		}
	}

	// Calls POST /auth/v1/factors.
	payload := map[string]any{
		"factor_type":   "totp",
		"friendly_name": body.FriendlyName,
	}
	type enrollStartResp struct {
		ID   string `json:"id"`
		Type string `json:"type"`
		TOTP struct {
			QRCode string `json:"qr_code"`
			Secret string `json:"secret"`
			URI    string `json:"uri"`
		} `json:"totp"`
	}
	var gotrueResp enrollStartResp
	hasAccess := readSupabaseAccessCookie(req) != ""
	hasRefresh := readSupabaseRefreshCookie(req) != ""
	status, err := r.supabaseCall(w, req, "POST", "/auth/v1/factors", payload, &gotrueResp)
	if err != nil {
		log.Printf("mfa.enroll_start: user=%s hasAccess=%v hasRefresh=%v err=%v", username, hasAccess, hasRefresh, err)
		writeErr(w, 502, "supabase: "+err.Error())
		return
	}
	if status != http.StatusOK {
		log.Printf("mfa.enroll_start: user=%s hasAccess=%v hasRefresh=%v status=%d body=%s", username, hasAccess, hasRefresh, status, truncate(fmt.Sprintf("%+v", gotrueResp), 200))
		writeErr(w, status, "supabase enroll status "+fmt.Sprint(status))
		return
	}
	r.auditEvent(req, username, "mfa.enroll_start", "factor_id="+gotrueResp.ID)
	writeJSON(w, map[string]any{
		"factor_id": gotrueResp.ID,
		"qr_code":   gotrueResp.TOTP.QRCode, // data URL SVG retornado por GoTrue
		"secret":    gotrueResp.TOTP.Secret,
		"uri":       gotrueResp.TOTP.URI,
	})
}

// ---------------- POST /api/auth/mfa/enroll-verify ----------------

func (r *Router) handleMFAEnrollVerify(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	username := auth.UserFrom(req)
	if username == "" {
		writeErr(w, 401, "unauthorized")
		return
	}
	type enrollVerifyReq struct {
		FactorID string `json:"factor_id"`
		Code     string `json:"code"`
	}
	var body enrollVerifyReq
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	body.Code = strings.TrimSpace(body.Code)
	if body.FactorID == "" || body.Code == "" {
		writeErr(w, 400, "factor_id and code required")
		return
	}

	// Step 1: challenge.
	type challengeResp struct {
		ID        string `json:"id"`
		ExpiresAt int64  `json:"expires_at"`
	}
	var ch challengeResp
	status, err := r.supabaseCall(w, req, "POST", "/auth/v1/factors/"+body.FactorID+"/challenge", nil, &ch)
	if err != nil {
		writeErr(w, 502, "challenge: "+err.Error())
		return
	}
	if status != http.StatusOK {
		writeErr(w, status, "challenge failed status "+fmt.Sprint(status))
		return
	}

	// Step 2: verify.
	verifyBody := map[string]any{"challenge_id": ch.ID, "code": body.Code}
	type verifyResp struct {
		AccessToken  string `json:"access_token,omitempty"`
		RefreshToken string `json:"refresh_token,omitempty"`
		ExpiresIn    int    `json:"expires_in,omitempty"`
	}
	var ver verifyResp
	status, err = r.supabaseCall(w, req, "POST", "/auth/v1/factors/"+body.FactorID+"/verify", verifyBody, &ver)
	if err != nil {
		writeErr(w, 502, "verify: "+err.Error())
		return
	}
	if status != http.StatusOK {
		r.auditEvent(req, username, "mfa.enroll_verify.fail", fmt.Sprintf("factor=%s status=%d", body.FactorID, status))
		writeErr(w, status, "verify failed (invalid code?)")
		return
	}

	// GoTrue returns a new aal2 session — update the cookies. Later requests use
	// the aal2 access_token; but since the JWT v2 stays local, this only matters
	// for future MFA endpoints.
	if ver.AccessToken != "" {
		setSupabaseAccessCookie(w, ver.AccessToken, ver.ExpiresIn)
		if ver.RefreshToken != "" {
			setSupabaseRefreshCookie(w, ver.RefreshToken)
		}
	}

	// Step 3: mint backup codes and persist them in data/mfa-backup-codes-<user>.json.
	codes, file, gerr := auth.GenerateBackupCodes(username, 10)
	if gerr != nil {
		writeErr(w, 500, "backup codes: "+gerr.Error())
		return
	}
	path := auth.BackupCodesPath(r.cfg.DataDir, username)
	store := auth.NewBackupCodesStore(path)
	if serr := store.Save(file); serr != nil {
		writeErr(w, 500, "save backup codes: "+serr.Error())
		return
	}

	// Sync the local mirror: HasTOTP=true for the admin UI list.
	r.setUserSupabaseMFA(username, true)

	r.auditEvent(req, username, "mfa.enroll_verify.ok", "factor_id="+body.FactorID)
	writeJSON(w, map[string]any{
		"verified":     true,
		"factor_id":    body.FactorID,
		"backup_codes": codes, // SHOWN ONCE — the front end tells the user to save them
	})
}

// ---------------- POST /api/auth/mfa/disable ----------------

func (r *Router) handleMFADisable(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	username := auth.UserFrom(req)
	if username == "" {
		writeErr(w, 401, "unauthorized")
		return
	}

	// Find the factor_id through GET /auth/v1/user.
	var user gotrueUserResp
	status, err := r.supabaseCall(w, req, "GET", "/auth/v1/user", nil, &user)
	if err != nil {
		writeErr(w, 502, "user lookup: "+err.Error())
		return
	}
	if status != http.StatusOK {
		writeErr(w, status, "user lookup status "+fmt.Sprint(status))
		return
	}
	factorID, _ := findFactorByType(&user, "totp")
	if factorID == "" {
		writeErr(w, 404, "no TOTP factor enrolled")
		return
	}

	// DELETE /auth/v1/factors/{id}.
	status, err = r.supabaseCall(w, req, "DELETE", "/auth/v1/factors/"+factorID, nil, nil)
	if err != nil {
		writeErr(w, 502, "delete factor: "+err.Error())
		return
	}
	if status != http.StatusOK && status != http.StatusNoContent {
		writeErr(w, status, "delete factor status "+fmt.Sprint(status))
		return
	}

	// Erase the backup codes.
	path := auth.BackupCodesPath(r.cfg.DataDir, username)
	store := auth.NewBackupCodesStore(path)
	_ = store.Delete()

	// With no 2FA there is no code to skip — revoke the trusted devices
	// (otherwise the cookie would stay "valid" but inert, and re-enabling MFA
	// would inherit the old trusts). Best effort.
	_ = r.trustedDeviceStoreFor(username).RevokeAll()

	// Local mirror: HasTOTP=false for the admin UI list.
	r.setUserSupabaseMFA(username, false)

	r.auditEvent(req, username, "mfa.disable", "factor_id="+factorID)
	writeJSON(w, map[string]any{"disabled": true})
}

// ---------------- GET /api/auth/mfa/status ----------------

func (r *Router) handleMFAStatus(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		writeErr(w, 405, "method not allowed")
		return
	}
	username := auth.UserFrom(req)
	if username == "" {
		writeErr(w, 401, "unauthorized")
		return
	}

	var user gotrueUserResp
	status, err := r.supabaseCall(w, req, "GET", "/auth/v1/user", nil, &user)
	if err != nil {
		writeErr(w, 502, "user lookup: "+err.Error())
		return
	}
	if status != http.StatusOK {
		writeErr(w, status, "user lookup status "+fmt.Sprint(status))
		return
	}
	factorID, factorStatus := findFactorByType(&user, "totp")
	enrolled := factorID != "" && factorStatus == "verified"

	// Backup codes count.
	path := auth.BackupCodesPath(r.cfg.DataDir, username)
	store := auth.NewBackupCodesStore(path)
	unused, _ := store.CountUnused()

	writeJSON(w, map[string]any{
		"enrolled":             enrolled,
		"factor_id":            factorID,
		"factor_status":        factorStatus,
		"backup_codes_unused":  unused,
		"backup_codes_present": unused > 0,
	})
}

// ---------------- internal helpers for the login flow ----------------

// supabaseUserMFAFactor queries GET /auth/v1/user and returns factor_id+status
// for a TOTP factor, OR empty when none is enrolled. Used in handleLogin to
// decide whether aal2 is required.
//
// This version is meant to be called WITHOUT a cookie already on the response
// (login does not have one yet). It takes the access_token directly.
func supabaseGetUserMFA(req *http.Request, sb *auth.SupabaseClient, accessToken string) (factorID, status string, err error) {
	if sb == nil || accessToken == "" {
		return "", "", fmt.Errorf("no supabase access token")
	}
	res, err := sb.AuthenticatedRequest(req.Context(), accessToken, "", "GET", "/auth/v1/user", nil)
	if err != nil {
		return "", "", err
	}
	if res.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("user lookup status %d", res.StatusCode)
	}
	var user gotrueUserResp
	if jerr := json.Unmarshal(res.Body, &user); jerr != nil {
		return "", "", jerr
	}
	for _, f := range user.Factors {
		if f.Type == "totp" {
			return f.ID, f.Status, nil
		}
	}
	return "", "", nil
}

// supabaseChallengeAndVerify runs challenge+verify as one composed call. Used
// in handleLogin to validate the Supabase TOTP when the user already has a
// factor enrolled.
//
// Returns (true, nil) on success; (false, nil) when the code is invalid; err on
// a network or parse problem.
func supabaseChallengeAndVerify(req *http.Request, sb *auth.SupabaseClient, accessToken, factorID, code string) (bool, error) {
	if sb == nil {
		return false, fmt.Errorf("no supabase client")
	}
	// Challenge
	res, err := sb.AuthenticatedRequest(req.Context(), accessToken, "", "POST", "/auth/v1/factors/"+factorID+"/challenge", nil)
	if err != nil {
		return false, err
	}
	if res.StatusCode != http.StatusOK {
		return false, fmt.Errorf("challenge status %d", res.StatusCode)
	}
	var ch struct {
		ID string `json:"id"`
	}
	if jerr := json.Unmarshal(res.Body, &ch); jerr != nil || ch.ID == "" {
		return false, fmt.Errorf("challenge parse")
	}
	// Verify
	verifyBody := map[string]any{"challenge_id": ch.ID, "code": code}
	res, err = sb.AuthenticatedRequest(req.Context(), accessToken, "", "POST", "/auth/v1/factors/"+factorID+"/verify", verifyBody)
	if err != nil {
		return false, err
	}
	if res.StatusCode == http.StatusOK {
		return true, nil
	}
	// 4xx = an invalid code (not a network error)
	if res.StatusCode >= 400 && res.StatusCode < 500 {
		return false, nil
	}
	return false, fmt.Errorf("verify status %d", res.StatusCode)
}

// backupCodeStorePath returns the store ready to use. A helper to cut boilerplate.
func (r *Router) backupCodeStoreFor(username string) *auth.BackupCodesStore {
	return auth.NewBackupCodesStore(filepath.Join(r.cfg.DataDir, fmt.Sprintf("mfa-backup-codes-%s.json", username)))
}

// trustedDeviceStoreFor returns the user's trusted-device store. It mirrors
// backupCodeStoreFor — one file per user under DataDir.
func (r *Router) trustedDeviceStoreFor(username string) *auth.TrustedDevicesStore {
	return auth.NewTrustedDevicesStore(auth.TrustedDevicesPath(r.cfg.DataDir, username))
}

// mobileRefreshStoreFor returns the user's mobile refresh-token store. It
// mirrors trustedDeviceStoreFor — one file per user under DataDir.
func (r *Router) mobileRefreshStoreFor(username string) *auth.MobileRefreshStore {
	return auth.NewMobileRefreshStore(auth.MobileRefreshStorePath(r.cfg.DataDir, username))
}

// guard: the "time" import is used somewhere — avoid unused.
var _ = time.Now
