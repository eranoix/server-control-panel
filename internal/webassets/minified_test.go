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
	entries, err := fs.ReadDir(sub, appDir)
	if err != nil {
		t.Fatalf("embed with no %s: %v", appDir, err)
	}

	var sources int
	for _, e := range entries {
		nome := e.Name()
		if !strings.HasSuffix(nome, ".js") || strings.HasSuffix(nome, ".min.js") {
			continue
		}
		sources++
		source := path.Join(appDir, nome)

		expected := path.Join(appDir, strings.TrimSuffix(nome, ".js")+".min.js")
		_, exists := fs.Stat(sub, expected)

		min, ok := MinifiedOf(source)
		if !ok {
			if exists == nil {
				// Runtime is safe (it falls back to the original), but this is a BUILD
				// DEFECT: there is an obsolete bundle embedded in the binary and every
				// client silently downloads twice as much — in a project that has a data
				// savings panel, silence will not do. Block it.
				t.Errorf("%s is in the embed but does NOT match %s (stamp missing or "+
					"from another source). Run `make minify`. Until then the binary carries "+
					"dead bytes and serves the original.", expected, source)
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
			t.Errorf("%s is STALE: stamp %s, but %s has sha256 %s.\n"+
				"Run `make minify`: this mismatch takes the "+
				"SPA down with a ReferenceError at Alpine boot.",
				min, stamp, source, hex.EncodeToString(sum[:]))
		}
	}

	if sources == 0 {
		t.Fatalf("no .js found in %s — did the app embed disappear?", appDir)
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
		{"correct stamp", "var x=1;\n" + stampPrefix + right + "\n", true},
		{"stamp from another source", "var x=2;\n" + stampPrefix + strings.Repeat("a", 64) + "\n", false},
		{"no stamp (legacy bundle)", "var x=1;\n", false},
		{"truncated stamp", "var x=1;\n" + stampPrefix + "deadbeef\n", false},
		{"non-hex stamp", "var x=1;\n" + stampPrefix + strings.Repeat("z", 64) + "\n", false},
		{"stamp in the middle, not at the end", stampPrefix + right + "\nvar x=1;\n", false},
	}

	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			stamp, ok := readStamp([]byte(c.min))
			valid := ok && stamp == right
			if valid != c.aceitavl {
				t.Errorf("valid=%v, expected %v: a minified file without proven "+
					"provenance must not replace the source", valid, c.aceitavl)
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

	source := path.Join(appDir, "00-shell.js")
	served := source
	if min, ok := MinifiedOf(source); ok {
		served = min
	}
	b, err := fs.ReadFile(sub, served)
	if err != nil {
		t.Fatalf("%s unreadable: %v", served, err)
	}
	if !strings.Contains(string(b), "adguardLoaded") {
		t.Errorf("index.html evaluates `adguardLoaded` at boot, but %s (the file "+
			"the server delivers) does not declare that state. Alpine turns this into a "+
			"ReferenceError and kills the whole app.", served)
	}
}
