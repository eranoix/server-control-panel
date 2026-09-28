package webassets

import "testing"

func TestPredictiveEchoDoesNotLie(t *testing.T) { runHarness(t, "test-predictive-echo.mjs") }

func TestHeavyScreensAreNotBornAtBoot(t *testing.T) { runHarness(t, "test-lazy-screens.mjs") }

func TestRecoveryTerminalSurvivesBadNetwork(t *testing.T) { runHarness(t, "test-recovery-term.mjs") }

func TestRecoveryClaudeStaysIndependent(t *testing.T) {
	runHarnessBash(t, "test-recovery-claude.sh")
}

func TestRecoveryTabsDoNotOverlap(t *testing.T) { runHarness(t, "test-recovery-tabs.mjs") }

func TestTerminalSizeSelfCorrects(t *testing.T) {
	runHarness(t, "test-reconciled-size.mjs")
}

func TestPanelRestoresHistoryOnOpen(t *testing.T) { runHarness(t, "test-panel-primer.mjs") }

func TestHistoryShowsInBrowser(t *testing.T) { runHarness(t, "test-primer-browser.mjs") }
