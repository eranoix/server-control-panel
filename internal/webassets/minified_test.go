package webassets

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"path"
	"strings"
	"testing"
)

func TestServedMinifiedMatchesCurrentSource(t *testing.T) {
	sub := SubFS()
	entries, err := fs.ReadDir(sub, appDir)
	if err != nil {
		t.Fatalf("embed with no %s: %v", appDir, err)
	}

	var sources int
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".js") || strings.HasSuffix(name, ".min.js") {
			continue
		}
		sources++
		source := path.Join(appDir, name)

		expected := path.Join(appDir, strings.TrimSuffix(name, ".js")+".min.js")
		_, exists := fs.Stat(sub, expected)

		min, ok := MinifiedOf(source)
		if !ok {
			if exists == nil {
				t.Errorf("%s is in the embed but does NOT match %s (stamp missing or "+
					"from another source). Run `make minify`. Until then the binary carries "+
					"dead bytes and serves the original.", expected, source)
			}
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

func TestStaleMinifiedIsNotServed(t *testing.T) {
	source := []byte("var x = 1;\n")
	sum := sha256.Sum256(source)
	right := hex.EncodeToString(sum[:])

	cases := []struct {
		name       string
		min        string
		acceptable bool
	}{
		{"correct stamp", "var x=1;\n" + stampPrefix + right + "\n", true},
		{"stamp from another source", "var x=2;\n" + stampPrefix + strings.Repeat("a", 64) + "\n", false},
		{"no stamp (legacy bundle)", "var x=1;\n", false},
		{"truncated stamp", "var x=1;\n" + stampPrefix + "deadbeef\n", false},
		{"non-hex stamp", "var x=1;\n" + stampPrefix + strings.Repeat("z", 64) + "\n", false},
		{"stamp in the middle, not at the end", stampPrefix + right + "\nvar x=1;\n", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stamp, ok := readStamp([]byte(c.min))
			valid := ok && stamp == right
			if valid != c.acceptable {
				t.Errorf("valid=%v, expected %v: a minified file without proven "+
					"provenance must not replace the source", valid, c.acceptable)
			}
		})
	}
}

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
