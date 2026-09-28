package pty

type clientSize struct {
	cols, rows   uint16
	acceptsFrame bool
}

func (c *sharedLog) registerSize(id int64, cols, rows uint16, acceptsFrame bool) (uint16, uint16, bool, []func(uint16, uint16)) {
	if cols < 2 || rows < 1 || cols > 1000 || rows > 1000 {
		return c.appliedCols, c.appliedRows, false, nil
	}
	if c.sizes == nil {
		c.sizes = map[int64]clientSize{}
	}
	c.sizes[id] = clientSize{cols: cols, rows: rows, acceptsFrame: acceptsFrame}
	return c.recompute()
}

func (c *sharedLog) forgetSize(id int64) (uint16, uint16, bool, []func(uint16, uint16)) {
	delete(c.sizes, id)
	delete(c.appliers, id)
	return c.recompute()
}

func (c *sharedLog) registerApplier(id int64, apply func(uint16, uint16)) (uint16, uint16) {
	sessionLogsMu.Lock()
	defer sessionLogsMu.Unlock()
	if c.appliers == nil {
		c.appliers = map[int64]func(uint16, uint16){}
	}
	c.appliers[id] = apply
	return c.appliedCols, c.appliedRows
}

func (c *sharedLog) recompute() (uint16, uint16, bool, []func(uint16, uint16)) {
	var targetCols, targetRows uint16
	var capCols, capRows uint16
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
