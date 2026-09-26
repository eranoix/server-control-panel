package mobilebff

import (
	"testing"
	"time"
)

func TestIdempotenciaLembraEDevolveOMesmoResultado(t *testing.T) {
	i := NovaIdempotencia(t.TempDir())

	if _, _, ok := i.Lembrada("sam:k1"); ok {
		t.Fatal("a key never seen must not be remembered")
	}

	i.Lembrar("sam:k1", `{"status":"ok"}`, 200)

	corpo, status, ok := i.Lembrada("sam:k1")
	if !ok {
		t.Fatal("the key should be remembered")
	}
	if corpo != `{"status":"ok"}` || status != 200 {
		t.Fatalf("result differs from what was stored: %q %d", corpo, status)
	}
}

func TestIdempotenciaNaoVazaEntreUsuarios(t *testing.T) {
	// The key includes the user on purpose: two devices can generate the same
	// identifier, and a result leaking between accounts would be worse than
	// having no idempotency at all.
	i := NovaIdempotencia(t.TempDir())
	i.Lembrar("sam:k1", `{"status":"ok"}`, 200)

	if _, _, ok := i.Lembrada("jordan:k1"); ok {
		t.Fatal("one user's key must not answer for another's")
	}
}

func TestIdempotenciaVenceDepoisDoPrazo(t *testing.T) {
	relogio := time.Now()
	i := NovaIdempotencia(t.TempDir())
	i.agora = func() time.Time { return relogio }

	i.Lembrar("sam:k1", `{"status":"ok"}`, 200)
	if _, _, ok := i.Lembrada("sam:k1"); !ok {
		t.Fatal("right after storing, it must be valid")
	}

	relogio = relogio.Add(25 * time.Hour)
	if _, _, ok := i.Lembrada("sam:k1"); ok {
		t.Fatal("once the deadline has passed, the key is no longer valid")
	}
}

func TestIdempotenciaAtravessaReinicioDoProcesso(t *testing.T) {
	// The case that motivates writing to disk: a deploy restarts the server,
	// and the retry from whoever was offline arrives AFTERWARDS. A table living
	// only in memory would lose exactly the keys that matter most.
	dir := t.TempDir()
	primeiro := NovaIdempotencia(dir)
	primeiro.Lembrar("sam:k1", `{"id":"abc"}`, 200)

	segundo := NovaIdempotencia(dir)
	corpo, status, ok := segundo.Lembrada("sam:k1")
	if !ok {
		t.Fatal("the key must survive a restart")
	}
	if corpo != `{"id":"abc"}` || status != 200 {
		t.Fatalf("result did not survive intact: %q %d", corpo, status)
	}
}

func TestIdempotenciaComArquivoCorrompidoComecaVaziaEmVezDeQuebrar(t *testing.T) {
	dir := t.TempDir()
	i := NovaIdempotencia(dir)
	i.Lembrar("sam:k1", "{}", 200)

	// Corrupt the file and load it again.
	if err := escrever(i.arquivo, "isto nao e json"); err != nil {
		t.Fatal(err)
	}
	outro := NovaIdempotencia(dir)
	if _, _, ok := outro.Lembrada("sam:k1"); ok {
		t.Fatal("a corrupted table must start empty")
	}
	// And it has to stay usable, not merely not crash.
	outro.Lembrar("sam:k2", "{}", 200)
	if _, _, ok := outro.Lembrada("sam:k2"); !ok {
		t.Fatal("after corruption, storing must still work")
	}
}

func TestIdempotenciaNilENoOp(t *testing.T) {
	// The BFF can run without a dataDir (the spec generator, for one). A nil
	// that blows up at runtime would be worse than the feature being absent.
	var i *Idempotencia
	i.Lembrar("sam:k1", "{}", 200)
	if _, _, ok := i.Lembrada("sam:k1"); ok {
		t.Fatal("nil remembers nothing")
	}
}
