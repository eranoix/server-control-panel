package webassets

import "testing"

// The predictive echo writes to the screen characters the server has not yet
// confirmed. That is only acceptable while the boundaries hold — full screen,
// password prompt, good network, edge of the line — and while the erase/repaint
// order inside the render flow holds. The harness runs against the real 00-shell.js.
func TestEcoPreditivoNaoMente(t *testing.T) { rodaHarness(t, "test-eco-preditivo.mjs") }

// The four heaviest screens (~196 KB of markup, 1,651 elements, 702 directives
// — 19% of the document) went behind a mount latch: they are not born at boot
// and, once opened, they are not destroyed. The harness measures that in a real
// browser, because only execution proves DOM.
func TestTelasPesadasNaoNascemNoBoot(t *testing.T) { rodaHarness(t, "test-telas-preguicosas.mjs") }

// The RECOVERY terminal is the screen you use when everything else is broken —
// and it was the most primitive client in the project: no reconnection, no
// backpressure, swallowing keystrokes in silence. The harness loads the page's
// real script with the DOM and the WebSocket doubled out, and exercises a
// genuine connection drop.
func TestTerminalDeRecuperacaoAguentaRedeRuim(t *testing.T) { rodaHarness(t, "test-recovery-term.mjs") }

// The independence of the recovery Claude is a property that is easy to lose
// without anyone noticing: it takes one ANTHROPIC_BASE_URL added "for
// consistency", or the host's .credentials.json mounted "so we don't
// authenticate twice", and the container starts depending on exactly what it
// exists to work around. The harness asserts the guarantees in the source and,
// where the container exists, in execution too.
func TestClaudeDeRecuperacaoContinuaIndependente(t *testing.T) {
	rodaHarnessBash(t, "test-recovery-claude.sh")
}

// The two /recovery tabs showed up TOGETHER on screen in production: the Claude
// notice and the host bar have an AUTHOR `display:flex`, which beats the user
// agent's `[hidden] { display:none }` — so `el.hidden = true` hid nothing and
// the screens split the height. CSS semantics do not show up in an expression
// test; this one renders the real page and measures visibility.
func TestAbasDoRecoveryNaoSeSobrepoem(t *testing.T) { rodaHarness(t, "test-recovery-abas.mjs") }

// Client and server disagreeing by ONE column leaves the screen illegible (the
// program draws for one width and xterm displays at another). What closes the
// class is the size being reasserted periodically instead of sent once — in both
// clients, which are separate code and therefore drift if nobody looks.
func TestTamanhoDoTerminalSeAutoCorrige(t *testing.T) {
	rodaHarness(t, "test-tamanho-reconciliado.mjs")
}

// The panel depended on the history block the SERVER re-emits on attach — and
// that block is skipped precisely in the sessions that matter (repainted flow).
// Opening the session on another computer showed a single page. The primer fixes
// that by fetching the raw log and replaying it into xterm itself, and what
// protects it is the ORDER (fetch, write, only then connect), the time cap and
// the conditional replay=0 — none of which shows on screen when it is right.
func TestPainelRecuperaOHistoricoAoAbrir(t *testing.T) { rodaHarness(t, "test-primer-do-painel.mjs") }

// AND THIS IS THE ONE THAT PROVES THE HISTORY SHOWS UP.
//
// It had been verified in layers — e2e against the real HostShell, source pins,
// invariants live, the route literal checked in the served bundle. None of them
// proves that xterm PAINTS. The harness brings up a test instance of the real
// server (its own port and dataDir; production untouched) and walks the reported
// path in a browser: it produces history, closes the whole browser, opens a fresh
// context and demands the lines that had already scrolled by.
//
// It found two defects no Go test would find: the last screen never arrived (the
// `.hist` only holds what SCROLLED) and the `dtach` attach clear wiped, on the
// client, what the primer had just painted.
func TestHistoricoApareceNoNavegador(t *testing.T) { rodaHarness(t, "test-primer-navegador.mjs") }
