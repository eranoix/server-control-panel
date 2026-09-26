package webassets

import "testing"

// Pasting a screenshot into the terminal uploaded the file TWICE and injected
// two paths into the pane. The cause was in no function at all: it was in the
// topology of the listeners — a paste captured on the container and another
// captured on the helper textarea, which is a descendant of it, receive the SAME
// Event instance, and the descendant's stopImmediatePropagation arrives far too
// late to cancel the ancestor that has already run.
//
// The harness runs in a real browser and dispatches a ClipboardEvent because
// propagation is DOM semantics: evaluating the functions in isolation, which is
// what the expression pins do, could never see this defect — that is exactly how
// it slipped through.
func TestPasteNoTerminalSobeUmaVezSo(t *testing.T) { rodaHarness(t, "test-paste-unico.mjs") }
