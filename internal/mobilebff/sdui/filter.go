package sdui

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

func DropFormFields(f *FormComponent, keep func(FormField) bool) {
	filtered := make([]FormField, 0, len(f.Fields))
	for _, field := range f.Fields {
		if keep(field) {
			filtered = append(filtered, field)
		}
	}
	f.Fields = filtered
}

func DropRowActions(t *TableComponent, keep func(ActionRef) bool) {
	filtered := make([]ActionRef, 0, len(t.RowActions))
	for _, a := range t.RowActions {
		if keep(a) {
			filtered = append(filtered, a)
		}
	}
	t.RowActions = filtered
}
