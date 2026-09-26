package pty

import (
	"os"
	"strings"
	"testing"
	"time"
)

// test clock — the lease is a rule about TIME, and testing it with
// `time.Sleep` would trade an assertion for a bet.
func comRelogio(t *testing.T) *time.Time {
	t.Helper()
	agora := time.Unix(1_700_000_000, 0)
	original := relogioDaSessao
	relogioDaSessao = func() time.Time { return agora }
	t.Cleanup(func() { relogioDaSessao = original })
	return &agora
}

// A SCRIBE THAT STOPPED DELIVERING MUST NOT HOLD THE POST.
//
// The first version elected the scribe by POSITION in the list and only promoted
// the next one when it LEFT. But a connection can stop delivering bytes without
// ever leaving — a hung `HostShell`, a dead `dtach` client, a network cut with
// no FIN. Then nobody writes and **the session log stops**.
//
// And it does not stop in harmless silence: it stops IN THE MIDDLE. Measured on
// the "Vpsm" session, the log ended exactly at `ESC[H ESC[J` — "erase the whole
// screen" — without the repaint that follows in the other fifteen occurrences of
// the same sequence in the file. The app replays the log to rebuild the screen,
// so it faithfully reproduced "erase everything" and stopped: a black screen,
// with all the content intact one scroll above.
func TestEscribaQueParouEhSubstituida(t *testing.T) {
	agora := comRelogio(t)
	dir := t.TempDir()

	zumbi, _, _, soltarZumbi := pegarLogDaSessao(dir, "sam", "Vpsm")
	defer soltarZumbi()
	vivo, _, _, soltarVivo := pegarLogDaSessao(dir, "sam", "Vpsm")
	defer soltarVivo()

	// The zombie takes the lease by writing the first chunk.
	_, _ = zumbi.Write([]byte("antes"))
	// ...and stops. The connection does NOT leave — that is what the earlier version missed.

	// The live one tries to write inside the lease: suppressed, or it would duplicate.
	_, _ = vivo.Write([]byte("|cedo"))

	// Once the lease has expired, the live one takes over with nobody having left.
	*agora = agora.Add(validadeDoArrendamento + time.Millisecond)
	_, _ = vivo.Write([]byte("|depois"))

	conteudo, err := os.ReadFile(sessionLogPath(dir, "sam", "Vpsm"))
	if err != nil {
		t.Fatalf("log was not written: %v", err)
	}
	if got := string(conteudo); got != "antes|depois" {
		t.Errorf("log = %q, want \"antes|depois\"", got)
	}
}

// WITH BOTH ALIVE, NOTHING DOUBLES.
//
// A session's clients are fed by the SAME `dtach` master and receive the same
// chunk milliseconds apart. With the scribe renewing, no other one comes close
// to finding the lease expired.
func TestDuasConexoesVivasNaoDobramOLog(t *testing.T) {
	agora := comRelogio(t)
	dir := t.TempDir()

	app, _, _, soltarApp := pegarLogDaSessao(dir, "sam", "s")
	defer soltarApp()
	web, _, _, soltarWeb := pegarLogDaSessao(dir, "sam", "s")
	defer soltarWeb()

	// Ten chunks, both receiving the same one, 5 ms apart.
	for i := 0; i < 10; i++ {
		_, _ = app.Write([]byte("\r\n"))
		*agora = agora.Add(5 * time.Millisecond)
		_, _ = web.Write([]byte("\r\n"))
		*agora = agora.Add(45 * time.Millisecond)
	}

	conteudo, _ := os.ReadFile(sessionLogPath(dir, "sam", "s"))
	if got := strings.Count(string(conteudo), "\r\n"); got != 10 {
		t.Errorf("wrote %d line breaks, wanted 10 — doubling the ones scrolled"+
			"empurra a tela inteira para o histórico e o app abre preto", got)
	}
}

// LEAVING POLITELY RELEASES THE LEASE AT ONCE.
//
// Waiting for the lease to expire would drop the first chunk after a tab closes
// into a hole of a second and a half, for no reason at all: whoever leaves knows
// they are leaving.
func TestSairSoltaOArrendamentoSemEsperar(t *testing.T) {
	comRelogio(t)
	dir := t.TempDir()

	primeira, _, _, soltarPrimeira := pegarLogDaSessao(dir, "sam", "s")
	segunda, _, _, soltarSegunda := pegarLogDaSessao(dir, "sam", "s")
	defer soltarSegunda()

	_, _ = primeira.Write([]byte("A"))
	soltarPrimeira()
	// The clock is stopped on purpose: with no waiting, the second one already writes.
	_, _ = segunda.Write([]byte("B"))

	conteudo, _ := os.ReadFile(sessionLogPath(dir, "sam", "s"))
	if string(conteudo) != "AB" {
		t.Errorf("log = %q, wanted \"AB\" — the succession waited for the lease to expire", string(conteudo))
	}
}

// THE CLEAR DTACH SENDS ON ATTACH DOES NOT GO INTO THE LOG.
//
// `dtach` writes `ESC[H ESC[J` onto the new client's screen when it attaches —
// the sequence is literally inside the binary. Addressed to a new screen it is
// correct; recorded into the session log it is poison, because the app replays
// the log to rebuild the screen and faithfully reproduces "erase everything".
//
// Measured on the "Vpsm" session: the file ended on exactly those six bytes,
// without the repaint that follows in the other fifteen occurrences — and the
// owner saw a black screen with the whole content intact one scroll above.
func TestLimpezaDeAttachDoDtachNaoEntraNoLog(t *testing.T) {
	comRelogio(t)
	dir := t.TempDir()

	w, _, _, soltar := pegarLogDaSessao(dir, "sam", "Vpsm")
	defer soltar()

	// dtach's first chunk: the clear, glued to the start of the real output.
	n, err := w.Write(append([]byte("\x1b[H\x1b[J"), []byte("ola")...))
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if n != 9 {
		t.Errorf("reported %d bytes; whoever writes into the tee must not be able to tell we trimmed", n)
	}
	// After the first chunk, a real clear from the PROGRAM does go through.
	_, _ = w.Write([]byte("\x1b[H\x1b[J|dele"))

	conteudo, _ := os.ReadFile(sessionLogPath(dir, "sam", "Vpsm"))
	if got := string(conteudo); got != "ola\x1b[H\x1b[J|dele" {
		t.Errorf("log = %q", got)
	}
}

// A clear ALONE in the first chunk does not go in either — and that is the real
// case: dtach writes only the clear, and the program does not repaint because
// the size did not change.
func TestLimpezaSozinhaNoPrimeiroBlocoSome(t *testing.T) {
	comRelogio(t)
	dir := t.TempDir()

	w, _, _, soltar := pegarLogDaSessao(dir, "sam", "s")
	defer soltar()
	_, _ = w.Write([]byte("antes"))
	soltar()

	w2, _, _, soltar2 := pegarLogDaSessao(dir, "sam", "s")
	defer soltar2()
	if n, _ := w2.Write([]byte("\x1b[H\x1b[J")); n != 6 {
		t.Errorf("reported %d; want 6", n)
	}

	conteudo, _ := os.ReadFile(sessionLogPath(dir, "sam", "s"))
	if string(conteudo) != "antes" {
		t.Errorf("log = %q — the attach erased the recorded screen", string(conteudo))
	}
}

// DTACH'S GOODBYE DOES NOT GO INTO THE LOG EITHER.
//
// On detach, `dtach` writes `ESC[999H \r\n [detached] \r\n`. The
// `ESC[999H` throws the cursor onto the last line and the `\n` there SCROLLS THE
// WHOLE SCREEN up. On the screen of whoever is leaving it is a useful message;
// in the session log it is worse than the attach clear, because it does not only
// erase, it scrolls.
//
// Measured on the "Vpsm" session: the app's own engine, fed with the log,
// returned 52 blank lines and `[detached]` on line 51. The owner's report, again
// and again: "the screen goes dark, but when you scroll the page the text appears".
func TestDespedidaDoDtachNaoEntraNoLog(t *testing.T) {
	comRelogio(t)
	dir := t.TempDir()

	w, _, _, soltar := pegarLogDaSessao(dir, "sam", "Vpsm")
	defer soltar()

	_, _ = w.Write([]byte("o que o programa pintou"))
	// The farewell, exactly as dtach sends it: all in one chunk.
	n, err := w.Write([]byte("\x1b[999H\r\n[detached]\r\n\x1b[?25h"))
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if n != 26 {
		t.Errorf("reported %d bytes; whoever writes into the tee must not be able to tell we trimmed", n)
	}
	// And nothing after it goes through.
	_, _ = w.Write([]byte("resto do adeus"))

	conteudo, _ := os.ReadFile(sessionLogPath(dir, "sam", "Vpsm"))
	if got := string(conteudo); got != "o que o programa pintou" {
		t.Errorf("log = %q — the pipe's goodbye got into the program's record", got)
	}
}

// The chunk carrying the goodbye may carry REAL output before it, and that stays.
func TestOQueVemAntesDaDespedidaEhPreservado(t *testing.T) {
	comRelogio(t)
	dir := t.TempDir()

	w, _, _, soltar := pegarLogDaSessao(dir, "sam", "s")
	defer soltar()

	_, _ = w.Write([]byte("ultima linha do programa\x1b[999H\r\n[detached]\r\n"))

	conteudo, _ := os.ReadFile(sessionLogPath(dir, "sam", "s"))
	if got := string(conteudo); got != "ultima linha do programa" {
		t.Errorf("log = %q", got)
	}
}

// THE SLICE SERVED TO THE APP DOES NOT END IN DTACH'S NOISE.
//
// The logs ALREADY RECORDED still have the attach clear and the goodbye inside
// them — the owner needs the history and will delete none of it, so fixing the
// past cannot be destructive. The file stays intact; what is trimmed is what GOES OUT.
func TestRecorteServidoNaoTerminaNoRuidoDoDtach(t *testing.T) {
	// Exactly the tail measured on the "Vpsm" session: two clears and the goodbye.
	cauda := "\x1b[H\x1b[J\x1b[H\x1b[J\x1b[999H\r\n[detached]\r\n\x1b[?25h"
	got := string(semRuidoDoDtachNoFim([]byte("o que o programa pintou" + cauda)))
	if got != "o que o programa pintou" {
		t.Errorf("slice = %q", got)
	}
}

// An OLD `[detached]`, with real output after it, is legitimate history and has
// to stay: trimming the middle would change what the person saw.
func TestDespedidaAntigaNoMeioDoLogNaoEhAparada(t *testing.T) {
	log := "antes\x1b[999H\r\n[detached]\r\n" + strings.Repeat("saida real depois ", 5)
	got := string(semRuidoDoDtachNoFim([]byte(log)))
	if got != log {
		t.Errorf("trimmed the middle of the log; slice = %q", got)
	}
}
