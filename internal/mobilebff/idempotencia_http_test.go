package mobilebff

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/config"
	ptysvc "server-control-panel/internal/pty"
	"server-control-panel/internal/sessionbackup"
)

type testOutput struct {
	Body struct {
		Value int `json:"valor"`
	}
}

func TestRememberResult_RunsOncePerKey(t *testing.T) {
	idem := NewIdempotency(t.TempDir())
	runs := 0
	run := func() (*testOutput, error) {
		runs++
		out := &testOutput{}
		out.Body.Value = runs
		return out, nil
	}

	first, err := rememberResult(idem, "sam:k1", run)
	if err != nil {
		t.Fatalf("primeira: %v", err)
	}
	second, err := rememberResult(idem, "sam:k1", run)
	if err != nil {
		t.Fatalf("segunda: %v", err)
	}

	if runs != 1 {
		t.Errorf("executed %d times, wanted 1 — the retry must not re-execute", runs)
	}
	if first.Body.Value != second.Body.Value {
		t.Errorf("retry returned %d, the first returned %d — it has to be the SAME result",
			second.Body.Value, first.Body.Value)
	}
}

func TestRememberResult_ErrorIsNotRemembered(t *testing.T) {
	// An action that failed has to be able to succeed on the retry: it was a
	// failure that put it in the queue. Remembering the error would make the
	// queue repeat the same defeat for 24 h.
	idem := NewIdempotency(t.TempDir())
	fail := true
	run := func() (*testOutput, error) {
		if fail {
			return nil, errors.New("caiu a rede do outro lado")
		}
		out := &testOutput{}
		out.Body.Value = 7
		return out, nil
	}

	if _, err := rememberResult(idem, "sam:k1", run); err == nil {
		t.Fatal("wanted an error on the first one")
	}
	fail = false
	out, err := rememberResult(idem, "sam:k1", run)
	if err != nil {
		t.Fatalf("the retry after an error has to be able to succeed: %v", err)
	}
	if out.Body.Value != 7 {
		t.Errorf("value = %d, wanted 7", out.Body.Value)
	}
}

func TestRememberResult_NoKeyAlwaysRuns(t *testing.T) {
	// Whoever does not send the header (the web panel, a curl) gets no special
	// path at all.
	idem := NewIdempotency(t.TempDir())
	runs := 0
	run := func() (*testOutput, error) {
		runs++
		return &testOutput{}, nil
	}
	_, _ = rememberResult(idem, "", run)
	_, _ = rememberResult(idem, "", run)
	if runs != 2 {
		t.Errorf("ran %d times, want 2", runs)
	}
}

func TestIdempotencyKey_DoesNotLeakAcrossAccounts(t *testing.T) {
	// The same key coming from two devices on different accounts must not
	// collide: a result leaking between accounts is worse than having no
	// idempotency at all.
	if a, b := idempotencyKey("sam", "k1"), idempotencyKey("teste", "k1"); a == b {
		t.Fatalf("keys from different accounts collided: %q", a)
	}
	if idempotencyKey("sam", "   ") != "" {
		t.Error("a blank header has to become an empty key (no-op)")
	}
	if idempotencyKey("", "k1") != "" {
		t.Error("with no user there is no key")
	}
}

// TestDeleteBackup_RetryReturnsFirst200 proves the whole chain:
// header → key → table → repeated response.
//
// The path under test is `?name=` (removing ONE session from inside the
// backup), which is the one that actually breaks on a retry: it reads the
// backup before touching it, and with the file already gone it answers 404.
// (Deleting the WHOLE backup is already idempotent in the store — `os.Remove`
// with `os.IsNotExist` tolerated —, so it proves nothing.) And 404 matters
// because the app's queue DISCARDS 4xx: without this the user would watch the
// action vanish as if it had failed.
func TestDeleteBackup_RetryReturnsFirst200(t *testing.T) {
	dataDir := t.TempDir()
	store := sessionbackup.New(dataDir)
	idem := NewIdempotency(dataDir)

	mux := http.NewServeMux()
	Mount(mux, Deps{
		Cfg:        &config.Config{DataDir: dataDir},
		SessionOwn: newTestOwnership(t),
		Idem:       idem,
	})

	seed := func(t *testing.T, id string) {
		t.Helper()
		if err := store.Write("sam", ptysvc.Backup{
			ID:       id,
			Created:  1757800000,
			Source:   sessionbackup.SourceManual,
			Sessions: []ptysvc.SessionSnapshot{{Name: "dev"}},
		}); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	doDelete := func(id, key string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodDelete, "/api/mobile/v1/terminal/backups/"+id+"?name=dev", nil)
		if key != "" {
			req.Header.Set(idempotencyHeader, key)
		}
		req = req.WithContext(auth.WithUser(req.Context(), "sam"))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	t.Run("with a key, the retry returns the first one's result", func(t *testing.T) {
		const id = "1757800000000000001"
		seed(t, id)
		first := doDelete(id, "fila-item-1")
		if first.Code != http.StatusOK {
			t.Fatalf("first: status = %d, wanted 200 (body=%s)", first.Code, first.Body.String())
		}
		second := doDelete(id, "fila-item-1")
		if second.Code != http.StatusOK {
			t.Fatalf("retry: status = %d, wanted 200 — without this the queue discards the action (body=%s)",
				second.Code, second.Body.String())
		}
		if first.Body.String() != second.Body.String() {
			t.Errorf("different bodies:\n first =%s\n second=%s", first.Body.String(), second.Body.String())
		}
	})

	t.Run("with no key, the retry stays 404", func(t *testing.T) {
		// Idempotency must not become a universal 200 that hides a real error:
		// whoever sends no key sees the world as it is.
		const id = "1757800000000000002"
		seed(t, id)
		if rec := doDelete(id, ""); rec.Code != http.StatusOK {
			t.Fatalf("first with no key: status = %d, wanted 200", rec.Code)
		}
		if rec := doDelete(id, ""); rec.Code != http.StatusNotFound {
			t.Errorf("second with no key: status = %d, wanted 404 (body=%s)", rec.Code, rec.Body.String())
		}
	})
}

// TestIdempotency_FileInDataDir makes sure the table writes where it should
// — an empty path would write into the process's working directory.
func TestIdempotency_FileInDataDir(t *testing.T) {
	dir := t.TempDir()
	idem := NewIdempotency(dir)
	idem.Remember("sam:k1", `{"Body":{"valor":1}}`, http.StatusOK)
	if _, err := os.Stat(filepath.Join(dir, "mobile-idempotencia.json")); err != nil {
		t.Fatalf("table was not written to dataDir: %v", err)
	}
}
