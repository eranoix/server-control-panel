package sdui

// The omission rule, in one sentence: components, fields and actions are
// omitted while the screen is assembled, per request, against the
// authenticated Viewer — PermissionHint exists only so a client with a stale
// cache can dim an affordance during the revalidation window; it is never the
// gate. A value the user may not see is never serialized as "hidden", because
// hidden-but-sent is readable by anyone with a proxy at hand (see
// Anti-Pattern 2). The three functions below are
// the only supported way to remove something from a screen: they rebuild the
// slice keeping only what passes keep — they never set a
// "hidden"/"disabled" flag, because that field does not exist in the
// vocabulary and never should.

// DropComponents rebuilds s.Components keeping only the components for which
// keep returns true. Beyond that, it removes any FormComponent left with zero
// Fields and any TableComponent left with zero Columns — a structurally empty
// component is a rendering bug, not a filtered screen, so it must never reach
// the client even if keep did keep it.
func DropComponents(s *Screen, keep func(Component) bool) {
	filtered := make([]Component, 0, len(s.Components))
	for _, c := range s.Components {
		if !keep(c) {
			continue
		}
		if isStructurallyEmpty(c) {
			continue
		}
		filtered = append(filtered, c)
	}
	s.Components = filtered
}

// isStructurallyEmpty reports whether c is a component with nothing left to
// render after field/column filtering.
func isStructurallyEmpty(c Component) bool {
	switch v := c.(type) {
	case FormComponent:
		return len(v.Fields) == 0
	case TableComponent:
		return len(v.Columns) == 0
	default:
		return false
	}
}

// DropFormFields rebuilds f.Fields keeping only the fields for which keep
// returns true. Call it before inserting f into Screen.Components — after
// that, DropComponents removes the whole form if f.Fields ended up empty.
func DropFormFields(f *FormComponent, keep func(FormField) bool) {
	filtered := make([]FormField, 0, len(f.Fields))
	for _, field := range f.Fields {
		if keep(field) {
			filtered = append(filtered, field)
		}
	}
	f.Fields = filtered
}

// DropRowActions rebuilds t.RowActions keeping only the actions for which
// keep returns true.
func DropRowActions(t *TableComponent, keep func(ActionRef) bool) {
	filtered := make([]ActionRef, 0, len(t.RowActions))
	for _, a := range t.RowActions {
		if keep(a) {
			filtered = append(filtered, a)
		}
	}
	t.RowActions = filtered
}
