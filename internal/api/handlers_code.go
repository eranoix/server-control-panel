package api

import (
	"context"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

const codeServerSocket = "/run/panel-code-server/code.sock"

func (r *Router) codeServerProxy() http.Handler {
	target, _ := url.Parse("http://unix")
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", codeServerSocket)
		},
	}
	proxy.FlushInterval = -1
	base := proxy.Director
	proxy.Director = func(req *http.Request) {
		req.URL.Path = strings.TrimPrefix(req.URL.Path, "/_code")
		if req.URL.Path == "" {
			req.URL.Path = "/"
		}
		base(req)
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		h := resp.Header
		if h.Get("X-Frame-Options") == "" {
			h.Set("X-Frame-Options", "SAMEORIGIN")
		}
		if h.Get("Referrer-Policy") == "" {
			h.Set("Referrer-Policy", "no-referrer")
		}
		return nil
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		http.Error(w, "editor (code-server) unavailable: "+err.Error(), http.StatusBadGateway)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if _, ok := r.mustPrimary(w, req); !ok {
			return
		}
		proxy.ServeHTTP(w, req)
	})
}
