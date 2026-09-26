package pty

// The session's size and who decides it.
//
// The session sits at the LARGEST of the clients that accept a rendered crop
// (`frame.go`); the smallest of the clients that do not is a CEILING, because
// they draw the raw stream and their grid must match the PTY exactly. The server
// ANNOUNCES the effective size ([notifySize]) so every client draws a grid of
// that size, not of its own window. Sizes are tracked PER SESSION, never per
// connection: a per-connection dedup kept the losing client from correcting
// itself. See [recompute].
type clientSize struct {
	cols, rows uint16
	// acceptsFrame: this client knows how to receive a RENDERED CROP of the
	// session's screen (`frame.go`) when its window is smaller than it. A client
	// that does not only knows how to draw the raw stream, and therefore stays a
	// CEILING on the session's size; see [recompute].
	acceptsFrame bool
}

// registerSize records what this connection wants and returns the session's
// EFFECTIVE size (the smallest among those attached), whether it changed, and
// who to notify.
//
// The appliers come out as a list so the caller fires them OUTSIDE the lock:
// applying means writing to a websocket (and an ioctl), and writing while
// holding the session mutex is how you invent a deadlock between two connections.
func (c *sharedLog) registerSize(id int64, cols, rows uint16, acceptsFrame bool) (uint16, uint16, bool, []func(uint16, uint16)) {
	// A degenerate size never gets in, and here that matters MORE than it did
	// under "whoever spoke last": there, a client sending 1x1 ruined only itself;
	// under the minimum, it drags the whole session down with it.
	if cols < 2 || rows < 1 || cols > 1000 || rows > 1000 {
		return c.appliedCols, c.appliedRows, false, nil
	}
	if c.sizes == nil {
		c.sizes = map[int64]clientSize{}
	}
	c.sizes[id] = clientSize{cols: cols, rows: rows, acceptsFrame: acceptsFrame}
	return c.recompute()
}

// forgetSize takes whoever left out of the calculation. Without this, a
// small client that closed would keep shrinking the session forever.
//
// THE CALLER HAS TO USE WHAT THIS RETURNS: dropping the appliers leaves the
// session stuck at the departed client's size.
func (c *sharedLog) forgetSize(id int64) (uint16, uint16, bool, []func(uint16, uint16)) {
	delete(c.sizes, id)
	delete(c.appliers, id)
	return c.recompute()
}

// registerApplier records HOW to put the session's effective size on this
// connection, and returns what is already in force.
//
// It is an APPLIER, not a notice: every connection runs `dtach -a` on a pty of
// ITS OWN, and the `dtach` master sees the size of THAT pty (born 0x0 in
// `pty.Start`), so the connection's pty must be resized, not just the browser told.
//
// That is why EVERY connection registers an applier, including one that did not
// ask for `size=1`: touching its pty is mandatory either way; telling the client
// is what is optional.
func (c *sharedLog) registerApplier(id int64, apply func(uint16, uint16)) (uint16, uint16) {
	sessionLogsMu.Lock()
	defer sessionLogsMu.Unlock()
	if c.appliers == nil {
		c.appliers = map[int64]func(uint16, uint16){}
	}
	c.appliers[id] = apply
	return c.appliedCols, c.appliedRows
}

// recompute decides the SESSION's size.
//
// With a per-session emulator (`history.go`) the server can COMPOSE a rendered
// crop for a smaller client, so the session follows the LARGEST client that
// accepts frames. A client that does not (an old app, the recovery screen) only
// draws the raw stream, so the session still has to fit it:
//
//	target  = LARGEST among those that accept frames
//	ceiling = SMALLEST among those that do not
//	session = min(target, ceiling)
//
// With nobody accepting frames this degenerates into the old minimum rule.
func (c *sharedLog) recompute() (uint16, uint16, bool, []func(uint16, uint16)) {
	var targetCols, targetRows uint16 // LARGEST among those that accept a frame
	var capCols, capRows uint16       // SMALLEST among those that do not
	for _, t := range c.sizes {
		if t.acceptsFrame {
			if t.cols > targetCols {
				targetCols = t.cols
			}
			if t.rows > targetRows {
				targetRows = t.rows
			}
			continue
		}
		if capCols == 0 || t.cols < capCols {
			capCols = t.cols
		}
		if capRows == 0 || t.rows < capRows {
			capRows = t.rows
		}
	}
	cols, rows := targetCols, targetRows
	if capCols > 0 && (cols == 0 || capCols < cols) {
		cols = capCols
	}
	if capRows > 0 && (rows == 0 || capRows < rows) {
		rows = capRows
	}
	if cols == 0 || rows == 0 {
		// Nobody attached with a known size: keep what is there. Touching the
		// PTY when the last client leaves would make the program re-lay out
		// against a screen nobody is watching, and the next attach would find
		// the frame half-done.
		return c.appliedCols, c.appliedRows, false, nil
	}
	if cols == c.appliedCols && rows == c.appliedRows {
		return cols, rows, false, nil
	}
	c.appliedCols, c.appliedRows = cols, rows
	appliers := make([]func(uint16, uint16), 0, len(c.appliers))
	for _, a := range c.appliers {
		appliers = append(appliers, a)
	}
	return cols, rows, true, appliers
}
