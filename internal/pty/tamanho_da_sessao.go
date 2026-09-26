package pty

// THE SIZE OF THE SESSION, AND WHO DECIDES IT.
//
// ⚠️ THIS HEADER DESCRIBES THE RULE AS IT WAS BEFORE per-client rendering.
// The rule has changed: today the session sits at the LARGEST of the clients
// that accept a rendered crop (`quadro.go`), and the minimum became a CEILING
// for the ones that do not. The text below stays here because it explains WHY
// the minimum was the only possible answer while the server had no screen —
// and it is that condition, not the rule, that changed. See [recalcula].
//
// ── WHAT USED TO HOLD, AND WHY ───────────────────────────────────────────
//
// THE PTY SIZE IS THE SMALLEST AMONG THE CLIENTS — AND THE SERVER SAYS WHICH.
//
// ## The two halves, and why one alone becomes a defect
//
// This is the classic terminal-multiplexer rule, and it has TWO
// halves. Copying only the first one was the
// mistake, and it showed up whole in the log:
//
//	21:28:10 client resize: 83x63 -> 72x60 (session "Aplicativo")
//	21:28:10 effective size (smallest among the clients): 66x60
//
// The app asked for 72 columns, the PTY stayed at 66, and the program wrapped
// its lines at 66 inside a 72-wide grid: everything in the wrong place.
//
// The missing half: **EVERY client draws a grid the size of the
// SESSION**, not of its own window. Whoever has the larger window sees the session with empty
// space around it — which is what anyone has already seen opening the same shared
// session in two windows of different sizes.
//
// So the minimum is not "the server decides and the client copes". It is **"the server
// decides AND SAYS SO, and the client draws that"**. Hence [avisarTamanho].
//
// ## Why a server-side emulator is NOT required
//
// I once recorded that only a per-session VT emulator in Go would solve this. It is
// not needed: the server renders nothing, it only announces. Rendering is still
// the client's job — it merely starts rendering the SESSION's grid.
//
// ## Why the SMALLEST, and not the last one
//
// It is the only rule that CONVERGES. Everybody agrees on `min`, nobody has to
// react to anyone else's choice, and the frame fits whole on everyone's screen. "The
// last one who spoke" does not converge — that is the definition of a race. "The largest" converges
// and is worse: it does not fit on the smallest screen, which starts seeing clipped lines.
//
// The price is known and explainable: opening the same session in a small window
// shrinks everyone's, and closing it gives the size back.
//
// ## The two attempts, and why the second one was wrong
//
// Before, `proxy` applied the size of whoever spoke, with a dedup kept PER
// CONNECTION. With two clients that never converged: the PTY stayed at the size of
// whoever spoke last, and the loser COULD NO LONGER CORRECT ITSELF, because its
// own dedup believed the size was already applied. That was the defect, and it is real.
//
// The fix was to switch to the MINIMUM among the clients. And it
// was wrong, for a reason that only showed up under measurement:
//
//	21:28:10 client resize: 83x63 → 72x60 (session "Aplicativo")
//	21:28:10 effective size (smallest among the clients): 66x60
//
// The app asked for 72 columns and the PTY stayed at 66, because of a smaller
// client attached. The remote program started wrapping its lines at 66 and the
// app drew on a 72-wide grid: everything landing in the wrong place.
//
// ## Why the minimum alone does not serve here
//
// A multiplexer that RENDERS can use the minimum on its own: it keeps the screen
// and draws
// a copy for each client, at each one's size. `dtach` does not render —
// it is a pipe. What comes out of the program goes RAW to the client's emulator, and
// that is why the client's grid has to be IDENTICAL to the PTY's. Any difference
// is garbage on screen, not a smaller frame.
//
// ## So: the last one who spoke wins, but EVERYONE gets to speak
//
// That is the nature of a pipe with several clients, and it is what `dtach` has always
// done. Whoever is looking is whoever just touched the window, and that is who has to
// be right. The others see a frame of the wrong size until they touch their own
// window — the client reasserts the size on every connection and on every change
// (`reafirmarTamanhoAoAbrir`), so there is always a gesture that fixes it.
//
// What does NOT come back is the original defect: the dedup is now against the size the
// PTY REALLY has, kept PER SESSION, and not against what this connection
// sent last. That is what lets the client that lost correct itself.
//
// The day this can truly be the minimum is the day the server
// keeps the screen (one Go emulator per session) and tells the client what the
// EFFECTIVE size is, so it can adjust its own grid. Then the minimum becomes
// correct for the same reason. Today it is not.
//
// ── THAT DAY CAME, AND THE CONCLUSION WAS ANOTHER ────────────────────────
//
// The per-session emulator now exists. With it, the right answer was not
// "the minimum works now": it was that the minimum stopped being necessary. If
// the server has the screen, it can COMPOSE a crop of it for the smaller client
// — and then the session can sit at the LARGEST, which is the size of whoever is
// actually working. The minimum survives only as a ceiling for the client that
// does not yet know how to receive a crop.
type tamanhoDoCliente struct {
	cols, rows uint16
	// aceitaQuadro: this client knows how to receive a RENDERED CROP of the
	// session's screen (`quadro.go`) when its window is smaller than it. A client
	// that does not only knows how to draw the raw stream, and therefore stays a
	// CEILING on the session's size — see [recalcula].
	aceitaQuadro bool
}

// registraTamanho records what this connection wants and returns the session's
// EFFECTIVE size (the smallest among those attached), whether it changed, and
// who to notify.
//
// The appliers come out as a list so the caller fires them OUTSIDE the lock:
// applying means writing to a websocket (and an ioctl), and writing while
// holding the session mutex is how you invent a deadlock between two connections.
func (c *logCompartilhado) registraTamanho(id int64, cols, rows uint16, aceitaQuadro bool) (uint16, uint16, bool, []func(uint16, uint16)) {
	// A degenerate size never gets in, and here that matters MORE than it did
	// under "whoever spoke last": there, a client sending 1x1 ruined only itself;
	// under the minimum, it drags the whole session down with it.
	if cols < 2 || rows < 1 || cols > 1000 || rows > 1000 {
		return c.aplicadoCols, c.aplicadoRows, false, nil
	}
	if c.tamanhos == nil {
		c.tamanhos = map[int64]tamanhoDoCliente{}
	}
	c.tamanhos[id] = tamanhoDoCliente{cols: cols, rows: rows, aceitaQuadro: aceitaQuadro}
	return c.recalcula()
}

// esqueceTamanho takes whoever left out of the calculation. Without this, a
// small client that closed would keep shrinking the session forever.
//
// THE CALLER HAS TO USE WHAT THIS RETURNS. For a while it did not: the
// `soltar` of [pegarLogDaSessao] called `esqueceTamanho(id)` and threw all four
// return values away. The test for this rule kept passing — it measures the
// function's return — and even so, in production, closing the small tab left
// the session stuck at its size forever. Measured end to end: after the 80x24
// client left, the program kept painting 24x80 while the other drew 38x110,
// and not even the heartbeat fixed it.
func (c *logCompartilhado) esqueceTamanho(id int64) (uint16, uint16, bool, []func(uint16, uint16)) {
	delete(c.tamanhos, id)
	delete(c.aplicadores, id)
	return c.recalcula()
}

// registraAplicador records HOW to put the session's effective size on this
// connection, and returns what is already in force.
//
// Note that it is an APPLIER, not a notice. It used to hold only "how to talk to
// the client", and that was the missing half: every connection has a `dtach -a`
// running on a pty of ITS OWN, and it is the size of THAT pty that the `dtach`
// master sees. Telling the browser without touching the connection's pty left
// the master arbitrating between clients that disagreed — and an arriving client
// was sized by nobody (the pty is born 0x0 in `pty.Start`).
//
// That is why EVERY connection registers an applier, including one that did not
// ask for `size=1`: touching its pty is mandatory either way; telling the client
// is what is optional.
func (c *logCompartilhado) registraAplicador(id int64, aplicar func(uint16, uint16)) (uint16, uint16) {
	logsDeSessaoMu.Lock()
	defer logsDeSessaoMu.Unlock()
	if c.aplicadores == nil {
		c.aplicadores = map[int64]func(uint16, uint16){}
	}
	c.aplicadores[id] = aplicar
	return c.aplicadoCols, c.aplicadoRows
}

// recalcula decides the SESSION's size.
//
// ## The rule was turned inside out, and the reason is that there is a screen now
//
// While the server only relayed bytes, the minimum was the only possible rule:
// the PTY has one size, everyone receives the same bytes, and the frame has to
// fit on the smallest screen — otherwise the smaller client sees clipped lines.
// The price was the owner of a 120-column desktop working at 53 because the
// phone was attached.
//
// With a per-session emulator (`historico.go`), the server can COMPOSE what the
// smaller client sees: a rendered crop, diffed line by line. So the session
// starts sitting at the LARGEST among the clients that know how to receive that
// crop.
//
// The minimum did not disappear — it became a CEILING. A client that does not
// accept frames (an old app, the recovery screen) only knows how to draw the raw
// stream, and for it the session still has to fit. So:
//
//	target  = LARGEST among those that accept frames
//	ceiling = SMALLEST among those that do not
//	session = min(target, ceiling)
//
// With nobody who accepts frames, this degenerates into exactly the old rule —
// which is what makes the change safe for anyone not yet updated.
func (c *logCompartilhado) recalcula() (uint16, uint16, bool, []func(uint16, uint16)) {
	var alvoCols, alvoRows uint16 // LARGEST among those that accept a frame
	var tetoCols, tetoRows uint16 // SMALLEST among those that do not
	for _, t := range c.tamanhos {
		if t.aceitaQuadro {
			if t.cols > alvoCols {
				alvoCols = t.cols
			}
			if t.rows > alvoRows {
				alvoRows = t.rows
			}
			continue
		}
		if tetoCols == 0 || t.cols < tetoCols {
			tetoCols = t.cols
		}
		if tetoRows == 0 || t.rows < tetoRows {
			tetoRows = t.rows
		}
	}
	cols, rows := alvoCols, alvoRows
	if tetoCols > 0 && (cols == 0 || tetoCols < cols) {
		cols = tetoCols
	}
	if tetoRows > 0 && (rows == 0 || tetoRows < rows) {
		rows = tetoRows
	}
	if cols == 0 || rows == 0 {
		// Nobody attached with a known size: keep what is there. Touching the
		// PTY when the last client leaves would make the program re-lay out
		// against a screen nobody is watching, and the next attach would find
		// the frame half-done.
		return c.aplicadoCols, c.aplicadoRows, false, nil
	}
	if cols == c.aplicadoCols && rows == c.aplicadoRows {
		return cols, rows, false, nil
	}
	c.aplicadoCols, c.aplicadoRows = cols, rows
	aplicadores := make([]func(uint16, uint16), 0, len(c.aplicadores))
	for _, a := range c.aplicadores {
		aplicadores = append(aplicadores, a)
	}
	return cols, rows, true, aplicadores
}
