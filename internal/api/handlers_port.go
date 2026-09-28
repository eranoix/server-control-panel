package api

import (
	"net/http"
	"net/http/httputil"
	"os/exec"
	"strconv"
	"strings"
)

const portForwardPrefix = "/_port/"

const selfPort = 8765

func parsePortPath(p string) (port int, rest string, needSlash, ok bool) {
	s := strings.TrimPrefix(p, portForwardPrefix)
	if s == p || s == "" {
		return 0, "", false, false
	}
	i := strings.IndexByte(s, '/')
	var digits string
	if i < 0 {
		digits = s
		needSlash = true
	} else {
		digits = s[:i]
		rest = s[i:]
	}
	n, err := strconv.Atoi(digits)
	if err != nil || n < 1 || n > 65535 {
		return 0, "", false, false
	}
	if rest == "" {
		rest = "/"
	}
	return n, rest, needSlash, true
}

func forwardablePort(n int) bool {
	return n >= 1024 && n <= 65535 && n != selfPort
}

func (r *Router) portForwardProxy() http.Handler {
	proxy := &httputil.ReverseProxy{
		FlushInterval: -1,
		Director: func(req *http.Request) {
			port, rest, _, ok := parsePortPath(req.URL.Path)
			if !ok {
				return
			}
			req.URL.Scheme = "http"
			req.URL.Host = "127.0.0.1:" + strconv.Itoa(port)
			req.URL.Path = rest
			if req.Header.Get("X-Forwarded-Proto") == "" {
				req.Header.Set("X-Forwarded-Proto", "https")
			}
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			http.Error(w, "port does not answer: "+err.Error(), http.StatusBadGateway)
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if _, ok := r.mustPrimary(w, req); !ok {
			return
		}
		port, _, needSlash, ok := parsePortPath(req.URL.Path)
		if !ok || !forwardablePort(port) {
			writeErr(w, 400, "invalid or non-forwardable port (use 1024-65535, except the panel's)")
			return
		}
		if needSlash {
			http.Redirect(w, req, req.URL.Path+"/", http.StatusFound)
			return
		}
		proxy.ServeHTTP(w, req)
	})
}

type devPort struct {
	Port int    `json:"port"`
	Addr string `json:"addr"`
	Proc string `json:"proc"`
}

func (r *Router) handleDevPorts(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.mustPrimary(w, req); !ok {
		return
	}
	ports := listListeningPorts()
	if ports == nil {
		ports = []devPort{}
	}
	writeJSON(w, map[string]any{"ports": ports, "prefix": portForwardPrefix})
}

func listListeningPorts() []devPort {
	out, err := exec.Command("ss", "-ltnpH").Output()
	if err != nil {
		return nil
	}
	seen := map[int]bool{}
	var res []devPort
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		local := f[3]
		idx := strings.LastIndexByte(local, ':')
		if idx < 0 {
			continue
		}
		host := local[:idx]
		if host != "127.0.0.1" && host != "[::1]" && host != "::1" {
			continue
		}
		n, err := strconv.Atoi(local[idx+1:])
		if err != nil || !forwardablePort(n) || seen[n] {
			continue
		}
		seen[n] = true
		proc := ""
		if i := strings.Index(line, `users:(("`); i >= 0 {
			rest := line[i+len(`users:(("`):]
			if j := strings.IndexByte(rest, '"'); j >= 0 {
				proc = rest[:j]
			}
		}
		res = append(res, devPort{Port: n, Addr: local[:idx], Proc: proc})
	}
	return res
}
