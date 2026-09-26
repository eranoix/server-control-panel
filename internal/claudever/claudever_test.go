package claudever

import "testing"

// Version comparison is the difference between a trustworthy indicator and one
// that lies. The first version of this code compared by EQUALITY and marked as
// "needs restart" anything that was AHEAD — the recovery container, which has its
// own newer installation, showed up on the list. An indicator that points at the
// wrong target is worse than no indicator at all.
func TestSoQuemEstaAtrasPrecisaReiniciar(t *testing.T) {
	casos := []struct {
		versao, instalada string
		atras             bool
		porque            string
	}{
		{"2.1.238", "2.1.240", true, "patch atrás"},
		{"2.1.240", "2.1.240", false, "mesma versão"},
		{"2.1.241", "2.1.240", false, "À FRENTE (foi o bug: container com instalação própria)"},
		{"2.0.999", "2.1.0", true, "minor atrás, apesar do patch alto"},
		{"2.1.9", "2.1.10", true, "10 > 9 — comparação numérica, não alfabética"},
		{"2.1.10", "2.1.9", false, "à frente pela mesma razão"},
		{"3.0.0", "2.9.9", false, "major à frente"},
		{"", "2.1.240", false, "sem versão: não dá para afirmar que está atrás"},
		{"2.1.240", "", false, "sem referência: idem"},
	}
	for _, c := range casos {
		if got := ehMaisVelha(c.versao, c.instalada); got != c.atras {
			t.Errorf("ehMaisVelha(%q, %q) = %v, wanted %v — %s", c.versao, c.instalada, got, c.atras, c.porque)
		}
	}
}

// The version comes from the PATH of the binary the process has open. Anything
// that is not a Claude version path has to return empty, otherwise the indicator
// would invent versions out of processes that are not the CLI.
func TestVersaoSaiDoCaminhoDoBinario(t *testing.T) {
	casos := map[string]string{
		"/root/.local/share/claude/versions/2.1.240": "2.1.240",
		"/opt/x/claude/versions/2.1.9":               "2.1.9",
		"/usr/bin/bash":                              "",
		"/root/.local/share/claude/versions/nightly": "", // not digits-and-dots
		"":                  "",
		"/claude/versions/": "",
	}
	for caminho, esperado := range casos {
		if got := versaoDoCaminho(caminho); got != esperado {
			t.Errorf("versaoDoCaminho(%q) = %q, wanted %q", caminho, got, esperado)
		}
	}
}

// PaiDe cuts the stat AFTER the last ')': the executable's name comes in
// parentheses and may contain spaces and parentheses. Splitting the whole line on
// spaces — the naive way — returns the wrong field precisely for processes with
// an odd name.
func TestPaiDeAguentaNomeDeProcessoComEspaco(t *testing.T) {
	dir := t.TempDir()
	raizAnterior := raizProc
	raizProc = dir
	defer func() { raizProc = raizAnterior }()

	escreve := func(pid, ppid int, nome string) {
		d := dir + "/" + itoa(pid)
		if err := mkdirAll(d); err != nil {
			t.Fatal(err)
		}
		linha := itoa(pid) + " (" + nome + ") S " + itoa(ppid) + " 1 1 0 -1 4194304 100 0 0 0"
		if err := writeFile(d+"/stat", linha); err != nil {
			t.Fatal(err)
		}
	}

	escreve(100, 42, "claude")
	escreve(101, 43, "meu app (v2)") // parentheses AND a space in the name
	escreve(102, 44, "a b c")

	for _, c := range []struct{ pid, ppid int }{{100, 42}, {101, 43}, {102, 44}} {
		if got := PaiDe(c.pid); got != c.ppid {
			t.Errorf("PaiDe(%d) = %d, wanted %d", c.pid, got, c.ppid)
		}
	}
}

// AncestralEm has to terminate even with an inconsistent /proc — a recycled PID
// has already produced a cycle in production in this kind of sweep.
func TestAncestralNaoEntraEmLaco(t *testing.T) {
	dir := t.TempDir()
	raizAnterior := raizProc
	raizProc = dir
	defer func() { raizProc = raizAnterior }()

	// 200 -> 201 -> 200 (cycle)
	for _, c := range []struct{ pid, ppid int }{{200, 201}, {201, 200}} {
		d := dir + "/" + itoa(c.pid)
		if err := mkdirAll(d); err != nil {
			t.Fatal(err)
		}
		if err := writeFile(d+"/stat", itoa(c.pid)+" (x) S "+itoa(c.ppid)+" 1"); err != nil {
			t.Fatal(err)
		}
	}
	feito := make(chan int, 1)
	go func() { feito <- AncestralEm(200, map[int]bool{999: true}) }()
	select {
	case got := <-feito:
		if got != 0 {
			t.Errorf("found ancestor %d where there was none", got)
		}
	case <-timeoutCurto():
		t.Fatal("AncestralEm did not terminate — an infinite loop with /proc in a cycle")
	}
}
