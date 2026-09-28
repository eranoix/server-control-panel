package scope

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"server-control-panel/internal/secrets"
)

func newTestVault(t *testing.T) (*UserVault, *secrets.Store, string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "secrets.vault")
	st, err := secrets.Open(path, "test-pass")
	if err != nil {
		t.Fatalf("secrets.Open: %v", err)
	}
	u := MustNew("sam")
	return NewUserVault(st, u), st, u.String() + ":", path
}

func TestValidGroupName(t *testing.T) {
	type tc struct {
		name      string
		isPrimary bool
		want      bool
	}
	cases := []tc{
		{"Project X", false, true},
		{"ops", false, true},
		{"northwind", true, true},
		{"", false, false},
		{"a:b", false, false},
		{"a/b", false, false},
		{"x\x00y", false, false},
		{"system", false, false},
		{"system", true, true},
		{"SYSTEM", false, false},
		{"System", true, true},
	}
	for _, c := range cases {
		if got := validGroupName(c.name, c.isPrimary); got != c.want {
			t.Errorf("validGroupName(%q, primary=%v) = %v, want %v", c.name, c.isPrimary, got, c.want)
		}
	}
}

func TestMetaKeyRejectedAndHidden(t *testing.T) {
	uv, _, _, _ := newTestVault(t)

	if validLogicalKey(metaLogicalKey) {
		t.Fatal("validLogicalKey(__meta__) = true, want false")
	}

	if err := uv.SetWithMeta("token", "abc", EntryMeta{Group: "ops", Type: "token"}); err != nil {
		t.Fatalf("SetWithMeta: %v", err)
	}
	for _, k := range uv.List() {
		if k == metaLogicalKey {
			t.Fatal("List() exposed the reserved __meta__ key")
		}
	}
	if len(uv.List()) != 1 || uv.List()[0] != "token" {
		t.Fatalf("List() = %v, want [token]", uv.List())
	}
}

func TestSetWithMetaAndListEntries(t *testing.T) {
	uv, _, _, _ := newTestVault(t)

	if err := uv.SetWithMeta("db_pass", "s3cr3t", EntryMeta{Group: "Project X", Type: "password", Notes: "prod"}); err != nil {
		t.Fatalf("SetWithMeta: %v", err)
	}
	entries := uv.ListEntries()
	if len(entries) != 1 {
		t.Fatalf("ListEntries len = %d, want 1", len(entries))
	}
	e := entries[0]
	if e.Key != "db_pass" || e.Group != "Project X" || e.Type != "password" || e.Notes != "prod" {
		t.Fatalf("entry = %+v, unexpected fields", e)
	}
	if e.CreatedAt == 0 || e.UpdatedAt == 0 {
		t.Fatalf("timestamps not stamped: %+v", e)
	}
	created := e.CreatedAt

	if err := uv.SetWithMeta("db_pass", "new-secret", EntryMeta{Group: "Project X", Type: "password"}); err != nil {
		t.Fatalf("SetWithMeta 2: %v", err)
	}
	e2 := uv.ListEntries()[0]
	if e2.CreatedAt != created {
		t.Fatalf("CreatedAt changed on update: %d -> %d", created, e2.CreatedAt)
	}
	if v, _ := uv.Get("db_pass"); v != "new-secret" {
		t.Fatalf("value not updated: %q", v)
	}
}

func TestDeleteRemovesValueAndMeta(t *testing.T) {
	uv, store, prefix, _ := newTestVault(t)

	if err := uv.SetWithMeta("a", "1", EntryMeta{Group: "g"}); err != nil {
		t.Fatalf("SetWithMeta a: %v", err)
	}
	if err := uv.SetWithMeta("b", "2", EntryMeta{Group: "g"}); err != nil {
		t.Fatalf("SetWithMeta b: %v", err)
	}

	if err := uv.Delete("a"); err != nil {
		t.Fatalf("Delete a: %v", err)
	}
	if _, ok := uv.Get("a"); ok {
		t.Fatal("value 'a' still present after Delete")
	}
	meta := uv.loadMeta()
	if _, ok := meta["a"]; ok {
		t.Fatal("meta for 'a' survived Delete")
	}
	if _, ok := meta["b"]; !ok {
		t.Fatal("meta for 'b' wrongly dropped")
	}

	if err := uv.Delete("b"); err != nil {
		t.Fatalf("Delete b: %v", err)
	}
	if _, ok := store.Get(prefix + metaLogicalKey); ok {
		t.Fatal("orphan __meta__ entry left after deleting all secrets")
	}
	if len(uv.List()) != 0 {
		t.Fatalf("List() = %v after wiping, want empty", uv.List())
	}
}

func TestCrossBinaryPlaintextStaysMap(t *testing.T) {
	uv, store, prefix, path := newTestVault(t)

	if err := uv.Set("waha_api_key", "legacy-value"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := uv.SetWithMeta("api_token", "tok", EntryMeta{Group: "ops", Type: "token", Notes: "n"}); err != nil {
		t.Fatalf("SetWithMeta: %v", err)
	}
	_ = store

	st2, err := secrets.Open(path, "test-pass")
	if err != nil {
		t.Fatalf("reopen failed — plaintext is no longer map[string]string: %v", err)
	}

	if v, ok := st2.Get(prefix + "waha_api_key"); !ok || v != "legacy-value" {
		t.Fatalf("raw Get waha_api_key = (%q,%v), want legacy-value", v, ok)
	}
	if v, ok := st2.Get(prefix + "api_token"); !ok || v != "tok" {
		t.Fatalf("raw Get api_token = (%q,%v), want tok", v, ok)
	}

	blob, ok := st2.Get(prefix + metaLogicalKey)
	if !ok || blob == "" {
		t.Fatal("reserved __meta__ blob missing after reopen")
	}
	var parsed map[string]EntryMeta
	if err := json.Unmarshal([]byte(blob), &parsed); err != nil {
		t.Fatalf("meta blob is not valid JSON: %v", err)
	}
	if parsed["api_token"].Group != "ops" {
		t.Fatalf("meta blob lost data: %+v", parsed)
	}

	uv2 := NewUserVault(st2, MustNew("sam"))
	for _, k := range uv2.List() {
		if k == metaLogicalKey {
			t.Fatal("List() exposed __meta__ after reopen")
		}
	}
}

func TestHandlerRejectsOversizeValue(t *testing.T) {
	uv, _, _, _ := newTestVault(t)
	h := uv.Handler(HandlerOpts{MaxValueBytes: 1024})

	big := strings.Repeat("x", 1025)
	body, _ := json.Marshal(map[string]string{"key": "k", "value": big})
	req := httptest.NewRequest(http.MethodPost, "/set", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize /set = %d, want 413", rec.Code)
	}
	if _, ok := uv.Get("k"); ok {
		t.Fatal("oversize value was stored despite 413")
	}
}

func TestHandlerSystemGroupGated(t *testing.T) {
	uv, _, _, _ := newTestVault(t)

	post := func(allowSystem bool, group string) int {
		h := uv.Handler(HandlerOpts{AllowSystemGroup: allowSystem})
		body, _ := json.Marshal(map[string]string{"key": "k", "value": "v", "group": group})
		req := httptest.NewRequest(http.MethodPost, "/set", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	if code := post(false, "system"); code != http.StatusBadRequest {
		t.Fatalf("non-primary system group = %d, want 400", code)
	}
	if code := post(true, "system"); code != http.StatusOK {
		t.Fatalf("primary system group = %d, want 200", code)
	}
}

func TestHandlerAuditEmitted(t *testing.T) {
	uv, _, _, _ := newTestVault(t)
	var events []string
	h := uv.Handler(HandlerOpts{Audit: func(action, target string) {
		events = append(events, action+" "+target)
	}})

	body, _ := json.Marshal(map[string]string{"key": "tok", "value": "v", "group": "ops"})
	req := httptest.NewRequest(http.MethodPost, "/set", bytes.NewReader(body))
	h.ServeHTTP(httptest.NewRecorder(), req)
	req = httptest.NewRequest(http.MethodGet, "/get?key=tok", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)
	body, _ = json.Marshal(map[string]string{"key": "tok"})
	req = httptest.NewRequest(http.MethodPost, "/delete", bytes.NewReader(body))
	h.ServeHTTP(httptest.NewRecorder(), req)

	want := []string{"secrets.set ops/tok", "secrets.reveal ops/tok", "secrets.delete ops/tok"}
	if strings.Join(events, "|") != strings.Join(want, "|") {
		t.Fatalf("audit events = %v, want %v", events, want)
	}
}

func TestHandlerListGroups(t *testing.T) {
	uv, _, _, _ := newTestVault(t)
	_ = uv.SetWithMeta("k1", "v", EntryMeta{Group: "ops"})
	_ = uv.SetWithMeta("k2", "v", EntryMeta{Group: "ops"})
	_ = uv.SetWithMeta("k3", "v", EntryMeta{})

	h := uv.Handler(HandlerOpts{})
	req := httptest.NewRequest(http.MethodGet, "/list", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var resp struct {
		Groups []GroupView `json:"groups"`
		Keys   []string    `json:"keys"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode /list: %v", err)
	}
	if len(resp.Keys) != 3 {
		t.Fatalf("keys = %v, want 3", resp.Keys)
	}
	if len(resp.Groups) != 2 {
		t.Fatalf("groups = %d, want 2", len(resp.Groups))
	}
	if resp.Groups[0].Name != "ops" || len(resp.Groups[0].Entries) != 2 {
		t.Fatalf("first group = %+v, want ops/2", resp.Groups[0])
	}
	if resp.Groups[1].Name != "" || len(resp.Groups[1].Entries) != 1 {
		t.Fatalf("last group = %+v, want empty/1", resp.Groups[1])
	}
}
