package webassets

import "testing"

func TestSavingsPanelSurvivesPartialState(t *testing.T) {
	runHarness(t, "test-datasaver-panel.mjs")
}
