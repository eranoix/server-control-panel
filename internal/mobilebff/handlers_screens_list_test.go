package mobilebff

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/mobilebff/sdui"
)

func init() {
	sdui.RegisterCatalog("test.screens.basic", sdui.GroupSystem, "Basic screen", func(sdui.Viewer) bool { return true })
	sdui.RegisterCatalog("test.screens.adminonly", sdui.GroupSecurity, "Admin only", func(v sdui.Viewer) bool { return v.IsAdmin() })
}

func listScreens(t *testing.T, username string) (*httptest.ResponseRecorder, ScreensResponse) {
	t.Helper()
	mux := http.NewServeMux()
	Mount(mux, Deps{Cfg: screensTestCfg()})

	req := httptest.NewRequest(http.MethodGet, "/api/mobile/v1/screens", nil)
	if username != "" {
		req = req.WithContext(auth.WithUser(req.Context(), username))
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	var resp ScreensResponse
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("json.Unmarshal(%s): %v", rec.Body.String(), err)
		}
	}
	return rec, resp
}

func sectionIDs(resp ScreensResponse) []string {
	ids := make([]string, 0, len(resp.Sections))
	for _, s := range resp.Sections {
		ids = append(ids, s.ID)
	}
	return ids
}

func TestListScreens_Unauthenticated(t *testing.T) {
	rec, _ := listScreens(t, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestListScreens_AdminSeesEverythingWithGroupAndLabel(t *testing.T) {
	rec, resp := listScreens(t, "screenadmin")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}

	byID := map[string]ScreenSection{}
	for _, s := range resp.Sections {
		byID[s.ID] = s
	}
	for _, want := range []string{"test.screens.basic", "test.screens.adminonly"} {
		s, ok := byID[want]
		if !ok {
			t.Fatalf("section %q missing for the admin: %v", want, sectionIDs(resp))
		}
		if s.Group == "" || s.Label == "" {
			t.Errorf("section %q came without group/label (%+v) — the selector cannot group it or name it", want, s)
		}
	}
}

func TestListScreens_NonAdminOmitsForbiddenSection(t *testing.T) {
	rec, resp := listScreens(t, "screenviewer")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}

	for _, s := range resp.Sections {
		if s.ID == "test.screens.adminonly" {
			t.Fatalf("non-admin received the admin-only section: %v", sectionIDs(resp))
		}
	}
	if strings.Contains(rec.Body.String(), "adminonly") {
		t.Errorf("the non-admin's body mentions the admin-only screen — filtering has to be by OMISSION, never an item marked as unavailable: %s", rec.Body.String())
	}

	var hasBasic bool
	for _, s := range resp.Sections {
		if s.ID == "test.screens.basic" {
			hasBasic = true
		}
	}
	if !hasBasic {
		t.Errorf("non-admin lost the section they CAN open: %v", sectionIDs(resp))
	}
}

func TestListScreens_SectionsIsAlwaysAnArray(t *testing.T) {
	rec, _ := listScreens(t, "screenviewer")
	body := rec.Body.String()
	if !strings.Contains(body, `"sections":[`) {
		t.Errorf("body does not carry `\"sections\":[` — sections must always be an array: %s", body)
	}
	if strings.Contains(body, `"sections":null`) {
		t.Errorf("sections came back null: %s", body)
	}
}

func TestListScreens_GroupsArriveContiguous(t *testing.T) {
	_, resp := listScreens(t, "screenadmin")

	seen := map[string]bool{}
	current := ""
	for _, s := range resp.Sections {
		if s.Group == current {
			continue
		}
		if seen[s.Group] {
			t.Errorf("group %q reappears after %q — items in a group must be contiguous", s.Group, current)
		}
		seen[s.Group] = true
		current = s.Group
	}
}
