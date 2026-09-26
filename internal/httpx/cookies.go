package httpx

import (
	"net/http"

	"server-control-panel/internal/auth"
)

// SetAuthCookie issues the JWT as an HttpOnly cookie (Path=/) so that
// same-origin navigations (the /browser/* iframes) authenticate without a
// header. The SPA keeps using the Bearer token from localStorage —
// auth.Middleware accepts both channels.
func SetAuthCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "vpsm_token",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(auth.TokenTTL.Seconds()),
	})
}

func ClearAuthCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     "vpsm_token",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// SetSupabaseAccessCookie stores the Supabase access_token in an HttpOnly
// cookie with Path=/api/auth/. Path-restricted so the cookie only travels to
// endpoints of the auth family (MFA, refresh, logout).
//
// TTL = the expires_in returned by GoTrue (1h by default). When it expires (a
// 401 on /api/auth/mfa/*), supabaseCallWithRefresh transparently attempts a
// refresh through vpsm_refresh.
func SetSupabaseAccessCookie(w http.ResponseWriter, access string, ttlSeconds int) {
	if access == "" {
		return
	}
	if ttlSeconds <= 0 {
		ttlSeconds = 3600
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "vpsm_supabase_access",
		Value:    access,
		Path:     "/api/auth/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   ttlSeconds,
	})
}

func ClearSupabaseAccessCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     "vpsm_supabase_access",
		Value:    "",
		Path:     "/api/auth/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func ReadSupabaseAccessCookie(req *http.Request) string {
	c, err := req.Cookie("vpsm_supabase_access")
	if err != nil || c == nil {
		return ""
	}
	return c.Value
}

// SetSupabaseRefreshCookie stores the Supabase refresh_token in an HttpOnly
// cookie with Path=/api/auth/. MaxAge=30 days matches the
// GOTRUE_JWT_EXP_REFRESH default.
func SetSupabaseRefreshCookie(w http.ResponseWriter, refresh string) {
	if refresh == "" {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "vpsm_refresh",
		Value:    refresh,
		Path:     "/api/auth/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   30 * 24 * 60 * 60,
	})
}

func ClearSupabaseRefreshCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     "vpsm_refresh",
		Value:    "",
		Path:     "/api/auth/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func ReadSupabaseRefreshCookie(req *http.Request) string {
	c, err := req.Cookie("vpsm_refresh")
	if err != nil || c == nil {
		return ""
	}
	return c.Value
}

// DeviceCookieName is the cookie holding the opaque trusted-device secret.
const DeviceCookieName = "vpsm_device"

// SetDeviceCookie stores the opaque trust secret in an HttpOnly cookie with
// Path=/api/auth/ (the same scope as the Supabase cookies — it covers
// /api/auth/login and does not leak into the other APIs). Secure:true is FIXED
// (do NOT derive it from req.TLS the way handlers_recovery.go does — behind
// Traefik the app sees plain HTTP and the cookie would vanish).
// MaxAge = the trust window (14d).
func SetDeviceCookie(w http.ResponseWriter, secret string) {
	if secret == "" {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     DeviceCookieName,
		Value:    secret,
		Path:     "/api/auth/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(auth.DeviceTrustTTL.Seconds()),
	})
}

func ClearDeviceCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     DeviceCookieName,
		Value:    "",
		Path:     "/api/auth/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func ReadDeviceCookie(req *http.Request) string {
	c, err := req.Cookie(DeviceCookieName)
	if err != nil || c == nil {
		return ""
	}
	return c.Value
}

// SetCookieFlag mirrors the presence of the auth cookie in a NON-HttpOnly
// cookie so the SPA can tell whether the cookie is still in force (HttpOnly is
// invisible to JS). It carries no credential — only the "yes/no" signal used
// by the post-deploy auto-refresh.
func SetCookieFlag(w http.ResponseWriter, set bool) {
	c := &http.Cookie{
		Name:     "vpsm_cookie_set",
		Value:    "1",
		Path:     "/",
		HttpOnly: false,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(auth.TokenTTL.Seconds()),
	}
	if !set {
		c.Value = ""
		c.MaxAge = -1
	}
	http.SetCookie(w, c)
}
