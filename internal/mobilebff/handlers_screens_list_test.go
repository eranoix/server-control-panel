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

// Catalogue entries for the two synthetic screens that
// handlers_screens_test.go already registers in this same test binary:
//
//   - "test.screens.basic" is visible to anyone authenticated, mirroring its
//     builder (which never refuses by role);
//   - "test.screens.adminonly" is admin-only, mirroring the builder that
//     returns sdui.ErrScreenNotFound for a non-admin.
//
// The pair is what lets us prove, over real HTTP, both halves of the contract:
// what shows up and — more importantly — what is OMITTED.
func init() {
	sdui.RegisterCatalog("test.screens.basic", sdui.GroupSistema, "Tela básica", func(sdui.Viewer) bool { return true })
	sdui.RegisterCatalog("test.screens.adminonly", sdui.GroupSeguranca, "Só admin", func(v sdui.Viewer) bool { return v.IsAdmin() })
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

// GET /screens with no session returns 401 through the same requireAuth that
// protects /screens/{id} — the catalogue reveals the administrative surface and
// can never be anonymous.
func TestListScreens_Unauthenticated(t *testing.T) {
	rec, _ := listScreens(t, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body=%s)", rec.Code, rec.Body.String())
	}
}

// The admin gets both sections, each with id, group and label — the three
// fields the picker needs to draw the grouped list.
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

// The CORE of the catalogue's RBAC: the admin-only section does not come marked
// as unavailable to the non-admin — it does not come at all. A disabled item
// would confirm the screen exists and would hand back the enumeration that the
// 404 of /screens/{id} exists to deny, so the test checks the RAW body: the id
// must not appear anywhere in the response, not even as loose text.
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

	var temBasic bool
	for _, s := range resp.Sections {
		if s.ID == "test.screens.basic" {
			temBasic = true
		}
	}
	if !temBasic {
		t.Errorf("non-admin lost the section they CAN open: %v", sectionIDs(resp))
	}
}

// `sections` always serializes as an array, never as null: the Kotlin client
// distinguishes an empty list (draw the empty state) from a missing field
// (malformed payload), and a `null` here would become a crash or a blank screen
// with no explanation.
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

// The sections arrive grouped and contiguous — the app draws one header per
// group in the order the items arrive, so a group that reappears after another
// one would produce two headers with the same name.
func TestListScreens_GroupsArriveContiguous(t *testing.T) {
	_, resp := listScreens(t, "screenadmin")

	seen := map[string]bool{}
	atual := ""
	for _, s := range resp.Sections {
		if s.Group == atual {
			continue
		}
		if seen[s.Group] {
			t.Errorf("group %q reappears after %q — items in a group must be contiguous", s.Group, atual)
		}
		seen[s.Group] = true
		atual = s.Group
	}
}
