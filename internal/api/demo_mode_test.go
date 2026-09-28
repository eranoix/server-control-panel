package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"sync"
	"testing"
)

func demoReq(method, path string) *http.Request {
	return httptest.NewRequest(method, "http://demo.example"+path, nil)
}

func TestDemoDeniesEveryMutatingMethod(t *testing.T) {
	for _, m := range []string{
		http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodPost,
	} {
		if demoAllows(demoReq(m, "/api/docker/containers")) {
			t.Errorf("%s allowed on a readable path — the method rule is not holding", m)
		}
	}
}

func TestDemoDeniesShellSurfaces(t *testing.T) {
	for _, p := range []string{
		"/recovery", "/recovery/ws/pty", "/recovery/action/reboot",
		"/_code/", "/_port/8080/", "/browser/", "/ws/shell", "/ws/container",
		"/api/terminal/sessions", "/api/admin/secrets", "/api/backup/list",
		"/api/users", "/api/claude/accounts", "/api/session/token",
	} {
		if demoAllows(demoReq(http.MethodGet, p)) {
			t.Errorf("GET %s allowed — this path reaches a shell or a secret", p)
		}
	}
}

func TestDemoDeniesWebSocketUpgradeOnAnyPath(t *testing.T) {
	r := demoReq(http.MethodGet, "/api/system/stats")
	r.Header.Set("Upgrade", "websocket")
	if demoAllows(r) {
		t.Fatal("websocket upgrade allowed on an allowlisted path")
	}
}

func TestDemoAllowsLoginAndReads(t *testing.T) {
	if !demoAllows(demoReq(http.MethodPost, "/api/auth/login")) {
		t.Error("login refused — nobody could enter the demo")
	}
	for _, p := range []string{"/", "/api/health", "/api/metrics", "/api/docker", "/_docs"} {
		if !demoAllows(demoReq(http.MethodGet, p)) {
			t.Errorf("GET %s refused — the demo would render empty", p)
		}
	}
}

func TestDemoDeniesUnknownAPIByDefault(t *testing.T) {
	for _, p := range []string{
		"/api/something-added-next-quarter", "/api/whatsapp/chats", "/api/videocall/rooms",
	} {
		if demoAllows(demoReq(http.MethodGet, p)) {
			t.Errorf("GET %s allowed — unknown routes must default to denied", p)
		}
	}
}

func TestDemoOffByDefault(t *testing.T) {
	t.Setenv("DEMO_MODE", "")
	demoOnce = sync.Once{}
	if demoMode() {
		t.Fatal("demo mode active without DEMO_MODE set")
	}
}

func TestDemoAllowsEveryRootAssetThePageLoads(t *testing.T) {
	html, err := os.ReadFile("../webassets/web/index.html")
	if err != nil {
		t.Skipf("index.html not found: %v", err)
	}
	re := regexp.MustCompile(`(?:href|src)="(/[^"/?]+\.(?:css|js))(?:\?[^"]*)?"`)
	found := 0
	for _, m := range re.FindAllSubmatch(html, -1) {
		found++
		if p := string(m[1]); !demoAllows(demoReq(http.MethodGet, p)) {
			t.Errorf("GET %s refused: the demo page loads it, so it would render broken", p)
		}
	}
	if found == 0 {
		t.Fatal("no root-level css/js found in index.html: the pattern no longer matches the page")
	}
}
