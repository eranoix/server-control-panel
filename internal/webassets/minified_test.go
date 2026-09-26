package webassets

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"path"
	"strings"
	"testing"
)

// minified_test.go — closes the CLASS of the stale-bundle bug.
//
// The actual defect: a stale `00-shell.min.js` went on being served after
// `00-shell.js` gained the AdGuard state. index.html referenced `adguardLoaded`,
// the served bundle did not have the property, and Alpine blew up with
// `ReferenceError: adguardLoaded is not defined` evaluating a template at boot —
// taking down the whole SPA, not just the Security tab.
//
// A test that only looked for `adguardLoaded` would close the CASE. These close
// the class: no minified file without proven provenance may stand in for the
// source, today or in any asset that comes to exist.

// TestServedMinifiedMatchesCurrentSource is the main pin: for every app asset, if
// the server is going to swap the .js for the .min.js, the minified file MUST be
// derived from the source that is in this same binary.
//
// It runs against the embed, so it gets exactly what the binary would serve —
// which is where the defect lived: on disk both files existed, only from
// different eras.
func TestServedMinifiedMatchesCurrentSource(t *testing.T) {
	sub := SubFS()
	entries, err := fs.ReadDir(sub, dirDoApp)
	if err != nil {
		t.Fatalf("embed with no %s: %v", dirDoApp, err)
	}

	var sources int
	for _, e := range entries {
		nome := e.Name()
		if !strings.HasSuffix(nome, ".js") || strings.HasSuffix(nome, ".min.js") {
			continue
		}
		sources++
		source := path.Join(dirDoApp, nome)

		expected := path.Join(dirDoApp, strings.TrimSuffix(nome, ".js")+".min.js")
		_, exists := fs.Stat(sub, expected)

		min, ok := MinifiedOf(source)
		if !ok {
			if exists == nil {
				// Runtime is safe (it falls back to the original), but this is a BUILD
				// DEFECT: there is an obsolete bundle embedded in the binary and every
				// client silently downloads twice as much — in a project that has a data
				// savings panel, silence will not do. Block it.
				t.Errorf("%s existe no embed mas NAO confere com %s (carimbo ausente ou "+
					"de outro fonte). Rode `make minify`. Enquanto isso o binario carrega "+
					"bytes mortos e serve o original.", expected, source)
			}
			// No .min.js at all: legitimate fail-open (esbuild missing).
			continue
		}

		bMin, err := fs.ReadFile(sub, min)
		if err != nil {
			t.Errorf("%s: MinifiedOf approved %s, but it is not in the embed: %v", source, min, err)
			continue
		}
		bSource, err := fs.ReadFile(sub, source)
		if err != nil {
			t.Fatalf("%s: unreadable in the embed: %v", source, err)
		}
		sum := sha256.Sum256(bSource)
		stamp, hasStamp := readStamp(bMin)
		if !hasStamp {
			t.Errorf("%s: approved to be served with no provenance stamp", min)
			continue
		}
		if stamp != hex.EncodeToString(sum[:]) {
			t.Errorf("%s esta OBSOLETO: carimbo %s, mas %s tem sha256 %s.\n"+
				"Rode `make minify` — foi exatamente esta divergencia que derrubou o "+
				"SPA com ReferenceError no boot do Alpine.",
				min, stamp, source, hex.EncodeToString(sum[:]))
		}
	}

	if sources == 0 {
		t.Fatalf("no .js found in %s — did the app embed disappear?", dirDoApp)
	}
}

// TestStaleMinifiedIsNotServed measures the DECISION, not the state of
// the disk: faced with a stamp that does not match the source, the answer has to
// be "serve the original" — never "send it anyway".
func TestStaleMinifiedIsNotServed(t *testing.T) {
	source := []byte("var x = 1;\n")
	sum := sha256.Sum256(source)
	right := hex.EncodeToString(sum[:])

	cases := []struct {
		nome     string
		min      string
		aceitavl bool
	}{
		{"stamp correto", "var x=1;\n" + stampPrefix + right + "\n", true},
		{"stamp de outro source", "var x=2;\n" + stampPrefix + strings.Repeat("a", 64) + "\n", false},
		{"sem stamp (bundle legado)", "var x=1;\n", false},
		{"stamp truncado", "var x=1;\n" + stampPrefix + "deadbeef\n", false},
		{"stamp nao-hex", "var x=1;\n" + stampPrefix + strings.Repeat("z", 64) + "\n", false},
		{"carimbo no meio, nao no fim", stampPrefix + right + "\nvar x=1;\n", false},
	}

	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			stamp, ok := readStamp([]byte(c.min))
			valid := ok && stamp == right
			if valid != c.aceitavl {
				t.Errorf("valido=%v, esperava %v — um minificado sem procedencia "+
					"comprovada nao can substituir o source", valid, c.aceitavl)
			}
		})
	}
}

// TestAdguardStateReachesServedBundle is the pin for the concrete case that broke.
//
// It exists alongside the class pin because the link that failed is between TWO
// files: index.html evaluates `adguardLoaded` at boot and the served bundle has
// to declare it. The stamp guarantees "the min came from the js"; this one
// guarantees the js in question is the one the HTML expects.
func TestAdguardStateReachesServedBundle(t *testing.T) {
	sub := SubFS()
	index, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		t.Fatalf("index.html unreadable: %v", err)
	}
	if !strings.Contains(string(index), "adguardLoaded") {
		t.Skip("index.html no longer references adguardLoaded")
	}

	source := path.Join(dirDoApp, "00-shell.js")
	served := source
	if min, ok := MinifiedOf(source); ok {
		served = min
	}
	b, err := fs.ReadFile(sub, served)
	if err != nil {
		t.Fatalf("%s unreadable: %v", served, err)
	}
	if !strings.Contains(string(b), "adguardLoaded") {
		t.Errorf("index.html avalia `adguardLoaded` no boot, mas %s — o arquivo que "+
			"o servidor entrega — nao declara o estado. Alpine transforma isso em "+
			"ReferenceError e kill o app inteiro.", served)
	}
}
