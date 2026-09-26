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

// newTestVault opens a throwaway encrypted store and binds it to user
// "sam", returning the vault, the raw store (for cross-binary assertions),
// the user prefix, and the on-disk path.
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
		{"Projeto X", false, true},
		{"infra", false, true},
		{"northwind", true, true},
		{"", false, false},    // empty rejected
		{"a:b", false, false}, // colon (namespace separator)
		{"a/b", false, false}, // slash (path component)
		{"x\x00y", false, false},
		{"system", false, false}, // reserved, non-primary
		{"system", true, true},   // reserved, primary OK
		{"SYSTEM", false, false}, // case-insensitive reserved
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

	// __meta__ is not an acceptable logical key for callers.
	if validLogicalKey(metaLogicalKey) {
		t.Fatal("validLogicalKey(__meta__) = true, want false")
	}

	if err := uv.SetWithMeta("token", "abc", EntryMeta{Group: "infra", Type: "token"}); err != nil {
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

	if err := uv.SetWithMeta("db_pass", "s3cr3t", EntryMeta{Group: "Projeto X", Type: "password", Notes: "prod"}); err != nil {
		t.Fatalf("SetWithMeta: %v", err)
	}
	entries := uv.ListEntries()
	if len(entries) != 1 {
		t.Fatalf("ListEntries len = %d, want 1", len(entries))
	}
	e := entries[0]
	if e.Key != "db_pass" || e.Group != "Projeto X" || e.Type != "password" || e.Notes != "prod" {
		t.Fatalf("entry = %+v, unexpected fields", e)
	}
	if e.CreatedAt == 0 || e.UpdatedAt == 0 {
		t.Fatalf("timestamps not stamped: %+v", e)
	}
	created := e.CreatedAt

	// Re-set: CreatedAt preserved, UpdatedAt refreshed (>= created).
	if err := uv.SetWithMeta("db_pass", "new-secret", EntryMeta{Group: "Projeto X", Type: "password"}); err != nil {
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
	// meta blob still exists (b remains) and no longer mentions 'a'.
	meta := uv.loadMeta()
	if _, ok := meta["a"]; ok {
		t.Fatal("meta for 'a' survived Delete")
	}
	if _, ok := meta["b"]; !ok {
		t.Fatal("meta for 'b' wrongly dropped")
	}

	// Teardown semantics: deleting the last secret must leave NO orphan
	// __meta__ entry on disk (mirrors whatsapp manager teardown loop).
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

// TestCrossBinaryPlaintextStaysMap is the certainty-anchoring test: after the
// new code writes metadata, the decrypted plaintext must STILL be a valid
// map[string]string — exactly what a legacy binary does on Open. We prove it
// by reopening the store (Open does json.Unmarshal into map[string]string and
// errors otherwise) and confirming the reserved blob is reachable raw but
// hidden from List().
func TestCrossBinaryPlaintextStaysMap(t *testing.T) {
	uv, store, prefix, path := newTestVault(t)

	// A "legacy" plain write (no meta), as the daemon does for waha_* keys.
	if err := uv.Set("waha_api_key", "legacy-value"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	// New-code write with metadata.
	if err := uv.SetWithMeta("api_token", "tok", EntryMeta{Group: "infra", Type: "token", Notes: "n"}); err != nil {
		t.Fatalf("SetWithMeta: %v", err)
	}
	_ = store

	// Reopen from disk: if the plaintext were no longer a map[string]string,
	// secrets.Open would return an unmarshal error here.
	st2, err := secrets.Open(path, "test-pass")
	if err != nil {
		t.Fatalf("reopen failed — plaintext is no longer map[string]string: %v", err)
	}

	// Original raw values intact (consumer invariant preserved).
	if v, ok := st2.Get(prefix + "waha_api_key"); !ok || v != "legacy-value" {
		t.Fatalf("raw Get waha_api_key = (%q,%v), want legacy-value", v, ok)
	}
	if v, ok := st2.Get(prefix + "api_token"); !ok || v != "tok" {
		t.Fatalf("raw Get api_token = (%q,%v), want tok", v, ok)
	}

	// The reserved blob is reachable directly (a legacy binary would see this
	// extra string key and ignore it)...
	blob, ok := st2.Get(prefix + metaLogicalKey)
	if !ok || blob == "" {
		t.Fatal("reserved __meta__ blob missing after reopen")
	}
	var parsed map[string]EntryMeta
	if err := json.Unmarshal([]byte(blob), &parsed); err != nil {
		t.Fatalf("meta blob is not valid JSON: %v", err)
	}
	if parsed["api_token"].Group != "infra" {
		t.Fatalf("meta blob lost data: %+v", parsed)
	}

	// ...but a UserVault over the reopened store hides it from List().
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

	// set
	body, _ := json.Marshal(map[string]string{"key": "tok", "value": "v", "group": "infra"})
	req := httptest.NewRequest(http.MethodPost, "/set", bytes.NewReader(body))
	h.ServeHTTP(httptest.NewRecorder(), req)
	// reveal
	req = httptest.NewRequest(http.MethodGet, "/get?key=tok", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)
	// delete
	body, _ = json.Marshal(map[string]string{"key": "tok"})
	req = httptest.NewRequest(http.MethodPost, "/delete", bytes.NewReader(body))
	h.ServeHTTP(httptest.NewRecorder(), req)

	want := []string{"secrets.set infra/tok", "secrets.reveal infra/tok", "secrets.delete infra/tok"}
	if strings.Join(events, "|") != strings.Join(want, "|") {
		t.Fatalf("audit events = %v, want %v", events, want)
	}
}

func TestHandlerListGroups(t *testing.T) {
	uv, _, _, _ := newTestVault(t)
	_ = uv.SetWithMeta("k1", "v", EntryMeta{Group: "infra"})
	_ = uv.SetWithMeta("k2", "v", EntryMeta{Group: "infra"})
	_ = uv.SetWithMeta("k3", "v", EntryMeta{}) // ungrouped

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
	// "infra" (2 entries) sorts before the empty group (1 entry, pushed last).
	if len(resp.Groups) != 2 {
		t.Fatalf("groups = %d, want 2", len(resp.Groups))
	}
	if resp.Groups[0].Name != "infra" || len(resp.Groups[0].Entries) != 2 {
		t.Fatalf("first group = %+v, want infra/2", resp.Groups[0])
	}
	if resp.Groups[1].Name != "" || len(resp.Groups[1].Entries) != 1 {
		t.Fatalf("last group = %+v, want empty/1", resp.Groups[1])
	}
}
