package pty

// historico.go — THE SESSION'S TRUE HISTORY, WRITTEN AS IT HAPPENS.
//
// ## What was still wrong after the recorder
//
// With the recorder (`gravador.go`) the log stopped having holes, and with the
// primer the panel loaded history again when the session was opened on another
// computer. But what the primer loads is the RAW BYTES, and in a program that
// redraws those are not history — they are the record of a drawing in progress.
//
// Replayed onto a new grid, those bytes duplicate. The reason is mechanical and
// has no fix on the reader's side: Ink (Claude Code) repaints by walking the
// cursor up with `ESC[nA`, and the CUU saturates at the first line of the SCREEN
// — it never reaches the scrollback. In a replay the cursor starts on an empty
// grid, the previous frame has already scrolled up and STAYS; the new one is
// painted below it. The same conversation shows up twice, three times, many
// times. That is the "the text is duplicated" of the report.
//
// ## The way out: whoever watched the session happen needs no replay
//
// The recorder is in the stream the whole time, and the session's size is known.
// So the server can keep an EMULATOR fed live: every `ESC[nA` lands exactly
// where the program meant it to, because the grid is the same one the program is
// looking at.
//
// And what matters is not its screen — it is what LEAVES it. A line that has
// scrolled off is finished: the program will not touch it again. Serialised back
// (`vt10x.EmBytes`), it is append-only text, which any terminal reproduces
// without ambiguity. The `<session>.hist` file is the sum of those lines: the
// history the person saw, once each.
//
// The CURRENT screen deliberately stays out of the file — it reaches the client
// through the attach repaint, painted by the program itself, which is what knows
// how to draw the whole of it. That way there is no overlap between what the
// primer writes and what the program paints next.
//
// ## Isolation: this is an observer, never a middleman
//
// The emulator does NOT sit between the PTY and the log. The recorder writes to
// the log first and only then feeds the screen. A panic in here (it is
// third-party code, patched) must not take the process down or stop anyone's
// terminal: the goroutine has a `recover`, and failing means switching off that
// session's history — never the session.

import (
	"bytes"
	"log"
	"sync"

	"server-control-panel/internal/pty/vt10x"
)

// colunasPadrao/linhasPadrao: the size the screen is born at, before the first
// client states its own. It is not a guess: it is the size `dtach` itself uses
// when nobody has spoken, so the screen starts out agreeing with the PTY.
const (
	colunasPadrao = 80
	linhasPadrao  = 24
)

// telaDaSessao is the emulator that follows a session and pours what leaves the
// screen into the history file.
type telaDaSessao struct {
	mu       sync.Mutex
	vt       *vt10x.State
	arquivo  *sessionLogWriter
	restante []byte // bytes of a rune split at the block boundary
	morta    bool   // a panic switched this screen off
	nome     string

	// Whoever wants to know the screen changed — the connections in frame mode
	// (`quadro.go`). `rolou` is how many lines left during the chunk: scrolling is
	// handled as scrolling, not as a repaint.
	assinantesMu sync.Mutex
	assinantes   map[int64]func(rolou int)
	proximoAssin int64
	// rolouNoBloco counts, WITHIN one alimenta, how many lines left.
	rolouNoBloco int
	// The notice `alimenta` left for `escoaAviso` to fire outside the lock.
	pendenteDeAviso  int
	temAvisoPendente bool
}

// assina registers whoever wants to be told the screen changed. It returns how
// to cancel — call that exactly once.
func (t *telaDaSessao) assina(fn func(rolou int)) func() {
	if t == nil || fn == nil {
		return func() {}
	}
	t.assinantesMu.Lock()
	if t.assinantes == nil {
		t.assinantes = map[int64]func(int){}
	}
	t.proximoAssin++
	id := t.proximoAssin
	t.assinantes[id] = fn
	t.assinantesMu.Unlock()
	return func() {
		t.assinantesMu.Lock()
		delete(t.assinantes, id)
		t.assinantesMu.Unlock()
	}
}

// avisaAssinantes fires OUTSIDE the screen's lock: whoever receives it will read
// the screen next, and reading while holding the writer's lock is how you invent
// a deadlock.
func (t *telaDaSessao) avisaAssinantes(rolou int) {
	t.assinantesMu.Lock()
	fns := make([]func(int), 0, len(t.assinantes))
	for _, f := range t.assinantes {
		fns = append(fns, f)
	}
	t.assinantesMu.Unlock()
	for _, f := range fns {
		f(rolou)
	}
}

// telaECursor returns a copy of the visible screen, the cursor and whether it is
// visible — what the frame compositor needs in order to draw.
func (t *telaDaSessao) telaECursor() ([][]vt10x.Glyph, vt10x.Cursor, bool) {
	if t == nil {
		return nil, vt10x.Cursor{}, false
	}
	t.mu.Lock()
	morta := t.morta
	t.mu.Unlock()
	if morta {
		return nil, vt10x.Cursor{}, false
	}
	return t.vt.TelaAtual(), t.vt.CursorAtual(), t.vt.CursorVisivel()
}

// tamanho returns the server screen's grid — the SESSION's grid.
func (t *telaDaSessao) tamanho() (cols, rows int) {
	if t == nil {
		return 0, 0
	}
	return t.vt.Tamanho()
}

func novaTelaDaSessao(dataDir, user, name string) *telaDaSessao {
	t := &telaDaSessao{
		vt:      vt10x.Novo(colunasPadrao, linhasPadrao),
		arquivo: abreEscritor(sessionHistPath(dataDir, user, name)),
		nome:    name,
	}
	t.vt.AoRolarParaFora(func(linhas [][]vt10x.Glyph) {
		// Called with the emulator's lock held: serialising is cheap (it is text)
		// and the writer is absolutely best-effort, like the rest of the tee.
		for _, l := range linhas {
			_, _ = t.arquivo.Write(vt10x.EmBytes(l))
		}
		t.rolouNoBloco += len(linhas)
	})
	return t
}

// alimenta hands the emulator the same bytes that went into the log.
//
// It carries over the partial rune left from the previous chunk: the recorder
// delivers whatever `read()` returned, and a multibyte character straddles that
// boundary all the time. Without this, every boundary would become a wrong
// character in the history.
func (t *telaDaSessao) alimenta(p []byte) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.morta {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			t.morta = true
			log.Printf("[pty] history of session %q turned off by a panic in the emulator: %v", t.nome, r)
		}
	}()
	dados := p
	if len(t.restante) > 0 {
		dados = append(append(make([]byte, 0, len(t.restante)+len(p)), t.restante...), p...)
		t.restante = nil
	}
	t.rolouNoBloco = 0
	n, err := t.vt.Write(dados)
	rolou := t.rolouNoBloco
	if err == nil && n < len(dados) {
		// A rune split at the end: keep it for the next chunk. The ceiling stops a
		// binary stream (which never completes a rune) growing this without limit.
		if sobra := dados[n:]; len(sobra) <= 8 {
			t.restante = append([]byte(nil), sobra...)
		}
	}
	// Notifying goes OUTSIDE the lock — see [avisaAssinantes]. The recover's
	// defer above has already run by the time this function returns, so the
	// notice does not leave here on a goroutine; it leaves on the way out, with
	// the lock released by the defer.
	t.pendenteDeAviso = rolou
	t.temAvisoPendente = true
}

// escoaAviso releases the notice `alimenta` left pending. Separate because
// `alimenta` holds the lock until it returns (the recover needs it) and
// notifying while holding it would invite a deadlock with whoever is about to
// READ the screen.
func (t *telaDaSessao) escoaAviso() {
	if t == nil {
		return
	}
	t.mu.Lock()
	tem, rolou := t.temAvisoPendente, t.pendenteDeAviso
	t.temAvisoPendente, t.pendenteDeAviso = false, 0
	t.mu.Unlock()
	if tem {
		t.avisaAssinantes(rolou)
	}
}

// redimensiona puts the server's screen at the session's EFFECTIVE size — the
// same one the program is looking at. That is what makes `ESC[nA` land in the
// right place and, in consequence, the history come out without repeated copies.
func (t *telaDaSessao) redimensiona(cols, rows uint16) {
	if t == nil || cols < 2 || rows < 1 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.morta {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			t.morta = true
			log.Printf("[pty] history of session %q turned off by a panic in the resize: %v", t.nome, r)
		}
	}()
	t.vt.Redimensiona(int(cols), int(rows))
}

// instantaneo serialises the VISIBLE lines of the screen, trimming the empty
// ones at the end.
//
// Why this is needed even with the history: the file only receives a line once
// it HAS SCROLLED off. What is still in view is not in there — and in an
// ordinary shell nothing repaints it when a new client attaches (`bash` redraws
// only the prompt line). Without this snapshot, whoever opens the session on
// another computer gets the whole history and loses exactly the last screen.
//
// Measured in the browser before it existed: the second PC saw the old lines and
// did NOT see the latest ones — a regression the primer introduced by asking for
// `replay=0`, because the raw chunk the server used to send covered that part.
func (t *telaDaSessao) instantaneo() []byte {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	morta := t.morta
	t.mu.Unlock()
	if morta {
		return nil
	}
	// On the alternate screen (vim, htop) the program is what redraws, in the
	// attach repaint: painting over it would be the duplication the wobble prevents.
	if t.vt.EmAltScreen() {
		return nil
	}
	linhas := t.vt.TelaAtual()
	fim := len(linhas)
	for fim > 0 && len(bytes.TrimSpace(stripANSIBytes(vt10x.EmBytes(linhas[fim-1])))) == 0 {
		fim--
	}
	var buf bytes.Buffer
	for _, l := range linhas[:fim] {
		buf.Write(vt10x.EmBytes(l))
	}
	return buf.Bytes()
}

func stripANSIBytes(b []byte) []byte { return []byte(stripANSI(string(b))) }

func (t *telaDaSessao) fecha() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	_ = t.arquivo.Close()
}

// HistoricoDaSessao returns the last maxBytes of the rendered history — what the
// panel writes into the xterm when it opens the session. Append-only text: no
// replay, no repeated copies, already at the session's width.
func HistoricoDaSessao(user, name string, maxBytes int) ([]byte, int) {
	if maxBytes <= 0 || maxBytes > maxRawLogTailBytes {
		maxBytes = maxRawLogTailBytes
	}
	dd := activeDD()
	corte, total := lerCauda(sessionHistPath(dd, user, name), maxBytes)
	if len(corte) < total {
		// It cut in the middle of a line: start on the next one. Half a line at
		// the top of the history is just dirt.
		if i := indiceDaProximaLinha(corte); i >= 0 {
			corte = corte[i:]
		}
	}
	// And the LIVE SCREEN at the end: the file covers what left, the snapshot
	// covers what is still in view. Only for a stream that does NOT redraw — in a
	// program that repaints, the one that draws the current screen is the program
	// itself, in the attach repaint, and painting over it would duplicate.
	if tela := telaDe(dd, user, name); tela != nil && !fluxoRecenteRepinta(dd, user, name) {
		if inst := tela.instantaneo(); len(inst) > 0 {
			corte = append(corte, inst...)
			total += len(inst)
		}
	}
	if total == 0 {
		return nil, 0
	}
	return corte, total
}

// fluxoRecenteRepinta reports whether the session's recent output came from a
// renderer that redraws. It uses the SAME calibrated classifier as
// `attachReplay` (see `limiteRepintura`), over the log's tail — reading the
// whole log to answer this on every attach would cost more and be no more exact.
func fluxoRecenteRepinta(dataDir, user, name string) bool {
	cauda, _ := lerCauda(sessionLogPath(dataDir, user, name), maxAttachReplayBytes)
	return len(cauda) > 0 && fluxoERepintado(cauda)
}

func indiceDaProximaLinha(b []byte) int {
	for i := 0; i+1 < len(b); i++ {
		if b[i] == '\r' && b[i+1] == '\n' {
			return i + 2
		}
	}
	return -1
}

// sessionHistPath is the path of the session's RENDERED history, sibling to the
// raw log. Separate files on purpose: one is the record of what went down the
// wire, the other is what the person saw.
func sessionHistPath(dataDir, user, name string) string {
	return sessionLogPath(dataDir, user, name) + ".hist"
}
