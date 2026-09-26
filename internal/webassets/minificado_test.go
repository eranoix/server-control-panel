package webassets

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"path"
	"strings"
	"testing"
)

// minificado_test.go — closes the CLASS of the stale-bundle bug.
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

// TestMinificadoServidoVemDoFonteAtual is the main pin: for every app asset, if
// the server is going to swap the .js for the .min.js, the minified file MUST be
// derived from the source that is in this same binary.
//
// It runs against the embed, so it gets exactly what the binary would serve —
// which is where the defect lived: on disk both files existed, only from
// different eras.
func TestMinificadoServidoVemDoFonteAtual(t *testing.T) {
	sub := SubFS()
	entradas, err := fs.ReadDir(sub, dirDoApp)
	if err != nil {
		t.Fatalf("embed with no %s: %v", dirDoApp, err)
	}

	var fontes int
	for _, e := range entradas {
		nome := e.Name()
		if !strings.HasSuffix(nome, ".js") || strings.HasSuffix(nome, ".min.js") {
			continue
		}
		fontes++
		fonte := path.Join(dirDoApp, nome)

		esperado := path.Join(dirDoApp, strings.TrimSuffix(nome, ".js")+".min.js")
		_, existe := fs.Stat(sub, esperado)

		min, ok := MinificadoDe(fonte)
		if !ok {
			if existe == nil {
				// Runtime is safe (it falls back to the original), but this is a BUILD
				// DEFECT: there is an obsolete bundle embedded in the binary and every
				// client silently downloads twice as much — in a project that has a data
				// savings panel, silence will not do. Block it.
				t.Errorf("%s existe no embed mas NAO confere com %s (carimbo ausente ou "+
					"de outro fonte). Rode `make minify`. Enquanto isso o binario carrega "+
					"bytes mortos e serve o original.", esperado, fonte)
			}
			// No .min.js at all: legitimate fail-open (esbuild missing).
			continue
		}

		bMin, err := fs.ReadFile(sub, min)
		if err != nil {
			t.Errorf("%s: MinificadoDe approved %s, but it is not in the embed: %v", fonte, min, err)
			continue
		}
		bFonte, err := fs.ReadFile(sub, fonte)
		if err != nil {
			t.Fatalf("%s: unreadable in the embed: %v", fonte, err)
		}
		soma := sha256.Sum256(bFonte)
		carimbo, temCarimbo := leCarimbo(bMin)
		if !temCarimbo {
			t.Errorf("%s: approved to be served with no provenance stamp", min)
			continue
		}
		if carimbo != hex.EncodeToString(soma[:]) {
			t.Errorf("%s esta OBSOLETO: carimbo %s, mas %s tem sha256 %s.\n"+
				"Rode `make minify` — foi exatamente esta divergencia que derrubou o "+
				"SPA com ReferenceError no boot do Alpine.",
				min, carimbo, fonte, hex.EncodeToString(soma[:]))
		}
	}

	if fontes == 0 {
		t.Fatalf("no .js found in %s — did the app embed disappear?", dirDoApp)
	}
}

// TestMinificadoObsoletoNaoESuperposto measures the DECISION, not the state of
// the disk: faced with a stamp that does not match the source, the answer has to
// be "serve the original" — never "send it anyway".
func TestMinificadoObsoletoNaoESuperposto(t *testing.T) {
	fonte := []byte("var x = 1;\n")
	soma := sha256.Sum256(fonte)
	certo := hex.EncodeToString(soma[:])

	casos := []struct {
		nome     string
		min      string
		aceitavl bool
	}{
		{"carimbo correto", "var x=1;\n" + carimboPrefixo + certo + "\n", true},
		{"carimbo de outro fonte", "var x=2;\n" + carimboPrefixo + strings.Repeat("a", 64) + "\n", false},
		{"sem carimbo (bundle legado)", "var x=1;\n", false},
		{"carimbo truncado", "var x=1;\n" + carimboPrefixo + "deadbeef\n", false},
		{"carimbo nao-hex", "var x=1;\n" + carimboPrefixo + strings.Repeat("z", 64) + "\n", false},
		{"carimbo no meio, nao no fim", carimboPrefixo + certo + "\nvar x=1;\n", false},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			carimbo, ok := leCarimbo([]byte(c.min))
			valido := ok && carimbo == certo
			if valido != c.aceitavl {
				t.Errorf("valido=%v, esperava %v — um minificado sem procedencia "+
					"comprovada nao pode substituir o fonte", valido, c.aceitavl)
			}
		})
	}
}

// TestEstadoDoAdguardChegaAoBundleServido is the pin for the concrete case that broke.
//
// It exists alongside the class pin because the link that failed is between TWO
// files: index.html evaluates `adguardLoaded` at boot and the served bundle has
// to declare it. The stamp guarantees "the min came from the js"; this one
// guarantees the js in question is the one the HTML expects.
func TestEstadoDoAdguardChegaAoBundleServido(t *testing.T) {
	sub := SubFS()
	index, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		t.Fatalf("index.html unreadable: %v", err)
	}
	if !strings.Contains(string(index), "adguardLoaded") {
		t.Skip("index.html no longer references adguardLoaded")
	}

	fonte := path.Join(dirDoApp, "00-shell.js")
	servido := fonte
	if min, ok := MinificadoDe(fonte); ok {
		servido = min
	}
	b, err := fs.ReadFile(sub, servido)
	if err != nil {
		t.Fatalf("%s unreadable: %v", servido, err)
	}
	if !strings.Contains(string(b), "adguardLoaded") {
		t.Errorf("index.html avalia `adguardLoaded` no boot, mas %s — o arquivo que "+
			"o servidor entrega — nao declara o estado. Alpine transforma isso em "+
			"ReferenceError e derruba o app inteiro.", servido)
	}
}
