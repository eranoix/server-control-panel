package screens

import "server-control-panel/internal/mobilebff/sdui"

// catalog.go — the two visibility predicates the catalog entries in this
// package use, and the rule for choosing between them.
//
// THE RULE, IN ONE SENTENCE: the predicate mirrors the BUILDER's gate, not
// the screen's apparent sensitivity.
//
//   - The builder returns sdui.ErrScreenNotFound for a non-admin
//     (by convention, the builders suffixed `...ForViewer`) → somenteAdmin.
//   - The builder ASSEMBLES the screen for any viewer, even with fewer
//     columns, fewer rows or fewer actions → sempreVisivel. The screen is
//     reachable, so hiding it from the picker would only create an
//     inexplicable empty page — the fine-grained filtering already happens
//     inside the Envelope itself.
//
// Choosing by "it looks sensitive" instead of by the real gate is exactly
// how catalog and builder drift apart. If the choice here is wrong,
// TestCatalogMatchesBuilderVisibility (internal/mobilebff/sdui) fails naming
// screen and role — it walks RegisteredScreens() × {admin, non-admin}
// requiring `present in the catalog ⟺ Build != ErrScreenNotFound`.

// sempreVisivel is the predicate for screens whose builder never refuses by
// role. It takes the Viewer and ignores it on purpose: the signature is
// sdui.RegisterCatalog's, and a constant predicate is an explicit assertion
// ("this screen is reachable by any authenticated user"), not an oversight.
func sempreVisivel(sdui.Viewer) bool { return true }

// somenteAdmin is the predicate for screens whose builder returns
// sdui.ErrScreenNotFound for a non-admin. It is the SAME check the builder
// makes (Viewer.IsAdmin), not a second rule written by hand.
func somenteAdmin(v sdui.Viewer) bool { return v.IsAdmin() }
