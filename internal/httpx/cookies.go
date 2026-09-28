package httpx

import (
	"net/http"

	"server-control-panel/internal/auth"
)

func SetAuthCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "panel_token",
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
		Name:     "panel_token",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func SetSupabaseAccessCookie(w http.ResponseWriter, access string, ttlSeconds int) {
	if access == "" {
		return
	}
	if ttlSeconds <= 0 {
		ttlSeconds = 3600
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "panel_supabase_access",
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
		Name:     "panel_supabase_access",
		Value:    "",
		Path:     "/api/auth/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func ReadSupabaseAccessCookie(req *http.Request) string {
	c, err := req.Cookie("panel_supabase_access")
	if err != nil || c == nil {
		return ""
	}
	return c.Value
}

func SetSupabaseRefreshCookie(w http.ResponseWriter, refresh string) {
	if refresh == "" {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "panel_refresh",
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
		Name:     "panel_refresh",
		Value:    "",
		Path:     "/api/auth/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func ReadSupabaseRefreshCookie(req *http.Request) string {
	c, err := req.Cookie("panel_refresh")
	if err != nil || c == nil {
		return ""
	}
	return c.Value
}

const DeviceCookieName = "panel_device"

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

func SetCookieFlag(w http.ResponseWriter, set bool) {
	c := &http.Cookie{
		Name:     "panel_cookie_set",
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
