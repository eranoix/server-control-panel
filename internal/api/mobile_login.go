package api

import (
	"fmt"
	"net/http"
	"strings"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/mobilebff"
)

func (r *Router) MobileLogin(req *http.Request, username, password, totpCode, deviceLabel string) (mobilebff.MobileLoginResult, error) {
	ip := auth.ClientIP(req)
	if !r.limiter.Allow(ip) {
		return mobilebff.MobileLoginResult{}, mobilebff.ErrMobileLoginRateLimited
	}

	username = strings.TrimSpace(username)
	if strings.Contains(username, "@") {
		username = strings.ToLower(username)
		if um := r.auth.UUIDMap(); um != nil {
			if canonical, ok := um.LookupByEmail(username); ok {
				username = canonical
			}
		}
	}

	if ok, _ := r.lockout.Allowed(username); !ok {
		return mobilebff.MobileLoginResult{}, mobilebff.ErrMobileLoginLocked
	}

	vres, vErr := r.auth.VerifyDetailed(req.Context(), username, password)
	if !vres.OK {
		r.lockout.RecordFailure(username)
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
		r.auditEvent(req, username, "mobile.login.fail", detail)
		return mobilebff.MobileLoginResult{}, mobilebff.ErrMobileLoginInvalidCredentials
	}

	mfa := r.verifyLoginMFA(req, vres, username, totpCode)
	switch mfa.result {
	case mfaUnavailableDenied:
		r.lockout.RecordFailure(username)
		r.auditEvent(req, username, "mobile.login.mfa.unavailable_denied", "")
		return mobilebff.MobileLoginResult{}, mobilebff.ErrMobileLoginMFAUnavailable
	case mfaCodeRequired:
		return mobilebff.MobileLoginResult{TOTPRequired: true}, nil
	case mfaCodeInvalid:
		r.lockout.RecordFailure(username)
		r.auditEvent(req, username, "mobile.login.mfa.fail", "factor="+mfa.factorID)
		return mobilebff.MobileLoginResult{}, mobilebff.ErrMobileLoginInvalidCode
	case mfaBackupUsed:
		r.auditEvent(req, username, "mobile.login.mfa.backup_used", "factor="+mfa.factorID)
	case mfaCodeVerified:
	}

	r.limiter.Reset(ip)
	r.lockout.RecordSuccess(username)

	tok, _, err := r.auth.Issue(username, &auth.IssueMeta{IP: ip, UserAgent: req.UserAgent()})
	if err != nil {
		return mobilebff.MobileLoginResult{}, fmt.Errorf("mobile login: issuing session: %w", err)
	}
	refreshTok, err := r.mobileRefreshStoreFor(username).Mint(username, deviceLabel)
	if err != nil {
		return mobilebff.MobileLoginResult{}, fmt.Errorf("mobile login: issuing refresh token: %w", err)
	}
	r.auditEvent(req, username, "mobile.login.ok", deviceLabel)

	return mobilebff.MobileLoginResult{
		AccessToken:  tok,
		RefreshToken: refreshTok,
		ExpiresIn:    int(auth.TokenTTL.Seconds()),
	}, nil
}

func (r *Router) MobileRefresh(refreshToken string) (mobilebff.MobileLoginResult, error) {
	username, ok := auth.ParseMobileRefreshUsername(refreshToken)
	if !ok {
		return mobilebff.MobileLoginResult{}, mobilebff.ErrMobileRefreshInvalid
	}
	if r.mobileRefreshLimiter != nil && !r.mobileRefreshLimiter.Allow(username) {
		return mobilebff.MobileLoginResult{}, mobilebff.ErrMobileLoginRateLimited
	}

	newRefresh, user, ok, err := r.mobileRefreshStoreFor(username).Rotate(refreshToken)
	if err != nil {
		return mobilebff.MobileLoginResult{}, fmt.Errorf("mobile refresh: rotating: %w", err)
	}
	if !ok {
		r.auditEventBackground(username, "mobile.refresh.invalid", "")
		return mobilebff.MobileLoginResult{}, mobilebff.ErrMobileRefreshInvalid
	}

	tok, _, err := r.auth.Issue(user, nil)
	if err != nil {
		return mobilebff.MobileLoginResult{}, fmt.Errorf("mobile refresh: issuing session: %w", err)
	}
	r.auditEventBackground(user, "mobile.refresh.ok", "")

	return mobilebff.MobileLoginResult{
		AccessToken:  tok,
		RefreshToken: newRefresh,
		ExpiresIn:    int(auth.TokenTTL.Seconds()),
	}, nil
}
