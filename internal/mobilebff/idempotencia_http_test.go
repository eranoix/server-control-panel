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

type saidaDeTeste struct {
	Body struct {
		Valor int `json:"valor"`
	}
}

func TestLembrarResultado_SoExecutaUmaVezPorChave(t *testing.T) {
	idem := NovaIdempotencia(t.TempDir())
	execucoes := 0
	roda := func() (*saidaDeTeste, error) {
		execucoes++
		out := &saidaDeTeste{}
		out.Body.Valor = execucoes
		return out, nil
	}

	primeira, err := lembrarResultado(idem, "sam:k1", roda)
	if err != nil {
		t.Fatalf("primeira: %v", err)
	}
	segunda, err := lembrarResultado(idem, "sam:k1", roda)
	if err != nil {
		t.Fatalf("segunda: %v", err)
	}

	if execucoes != 1 {
		t.Errorf("executed %d times, wanted 1 — the retry must not re-execute", execucoes)
	}
	if primeira.Body.Valor != segunda.Body.Valor {
		t.Errorf("retry returned %d, the first returned %d — it has to be the SAME result",
			segunda.Body.Valor, primeira.Body.Valor)
	}
}

func TestLembrarResultado_ErroNaoELembrado(t *testing.T) {
	// An action that failed has to be able to succeed on the retry: it was a
	// failure that put it in the queue. Remembering the error would make the
	// queue repeat the same defeat for 24 h.
	idem := NovaIdempotencia(t.TempDir())
	falhar := true
	roda := func() (*saidaDeTeste, error) {
		if falhar {
			return nil, errors.New("caiu a rede do outro lado")
		}
		out := &saidaDeTeste{}
		out.Body.Valor = 7
		return out, nil
	}

	if _, err := lembrarResultado(idem, "sam:k1", roda); err == nil {
		t.Fatal("wanted an error on the first one")
	}
	falhar = false
	out, err := lembrarResultado(idem, "sam:k1", roda)
	if err != nil {
		t.Fatalf("the retry after an error has to be able to succeed: %v", err)
	}
	if out.Body.Valor != 7 {
		t.Errorf("value = %d, wanted 7", out.Body.Valor)
	}
}

func TestLembrarResultado_SemChaveExecutaSempre(t *testing.T) {
	// Whoever does not send the header (the web panel, a curl) gets no special
	// path at all.
	idem := NovaIdempotencia(t.TempDir())
	execucoes := 0
	roda := func() (*saidaDeTeste, error) {
		execucoes++
		return &saidaDeTeste{}, nil
	}
	_, _ = lembrarResultado(idem, "", roda)
	_, _ = lembrarResultado(idem, "", roda)
	if execucoes != 2 {
		t.Errorf("ran %d times, want 2", execucoes)
	}
}

func TestChaveDeIdempotencia_NaoVazaEntreContas(t *testing.T) {
	// The same key coming from two devices on different accounts must not
	// collide: a result leaking between accounts is worse than having no
	// idempotency at all.
	if a, b := chaveDeIdempotencia("sam", "k1"), chaveDeIdempotencia("teste", "k1"); a == b {
		t.Fatalf("keys from different accounts collided: %q", a)
	}
	if chaveDeIdempotencia("sam", "   ") != "" {
		t.Error("a blank header has to become an empty key (no-op)")
	}
	if chaveDeIdempotencia("", "k1") != "" {
		t.Error("with no user there is no key")
	}
}

// TestDeleteBackup_RetentativaDevolveO200DaPrimeira proves the whole chain:
// header → key → table → repeated response.
//
// The path under test is `?name=` (removing ONE session from inside the
// backup), which is the one that actually breaks on a retry: it reads the
// backup before touching it, and with the file already gone it answers 404.
// (Deleting the WHOLE backup is already idempotent in the store — `os.Remove`
// with `os.IsNotExist` tolerated —, so it proves nothing.) And 404 matters
// because the app's queue DISCARDS 4xx: without this the user would watch the
// action vanish as if it had failed.
func TestDeleteBackup_RetentativaDevolveO200DaPrimeira(t *testing.T) {
	dataDir := t.TempDir()
	store := sessionbackup.New(dataDir)
	idem := NovaIdempotencia(dataDir)

	mux := http.NewServeMux()
	Mount(mux, Deps{
		Cfg:        &config.Config{DataDir: dataDir},
		SessionOwn: newTestOwnership(t),
		Idem:       idem,
	})

	semeia := func(t *testing.T, id string) {
		t.Helper()
		if err := store.Write("sam", ptysvc.Backup{
			ID:       id,
			Created:  1757800000,
			Source:   sessionbackup.OrigemManual,
			Sessions: []ptysvc.SessionSnapshot{{Name: "dev"}},
		}); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	apaga := func(id, chave string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodDelete, "/api/mobile/v1/terminal/backups/"+id+"?name=dev", nil)
		if chave != "" {
			req.Header.Set(cabecalhoIdempotencia, chave)
		}
		req = req.WithContext(auth.WithUser(req.Context(), "sam"))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	t.Run("with a key, the retry returns the first one's result", func(t *testing.T) {
		const id = "1757800000000000001"
		semeia(t, id)
		primeira := apaga(id, "fila-item-1")
		if primeira.Code != http.StatusOK {
			t.Fatalf("first: status = %d, wanted 200 (body=%s)", primeira.Code, primeira.Body.String())
		}
		segunda := apaga(id, "fila-item-1")
		if segunda.Code != http.StatusOK {
			t.Fatalf("retry: status = %d, wanted 200 — without this the queue discards the action (body=%s)",
				segunda.Code, segunda.Body.String())
		}
		if primeira.Body.String() != segunda.Body.String() {
			t.Errorf("different bodies:\n first =%s\n second=%s", primeira.Body.String(), segunda.Body.String())
		}
	})

	t.Run("with no key, the retry stays 404", func(t *testing.T) {
		// Idempotency must not become a universal 200 that hides a real error:
		// whoever sends no key sees the world as it is.
		const id = "1757800000000000002"
		semeia(t, id)
		if rec := apaga(id, ""); rec.Code != http.StatusOK {
			t.Fatalf("first with no key: status = %d, wanted 200", rec.Code)
		}
		if rec := apaga(id, ""); rec.Code != http.StatusNotFound {
			t.Errorf("second with no key: status = %d, wanted 404 (body=%s)", rec.Code, rec.Body.String())
		}
	})
}

// TestIdempotencia_ArquivoNoDataDir makes sure the table writes where it should
// — an empty path would write into the process's working directory.
func TestIdempotencia_ArquivoNoDataDir(t *testing.T) {
	dir := t.TempDir()
	idem := NovaIdempotencia(dir)
	idem.Lembrar("sam:k1", `{"Body":{"valor":1}}`, http.StatusOK)
	if _, err := os.Stat(filepath.Join(dir, "mobile-idempotencia.json")); err != nil {
		t.Fatalf("table was not written to dataDir: %v", err)
	}
}
