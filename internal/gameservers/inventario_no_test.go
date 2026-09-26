package gameservers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fonteFalsa is a fake node inventory, with the bare minimum of the interface.
type fonteFalsa map[string]DestinoNo

func (f fonteFalsa) NoPorID(id string) (DestinoNo, bool) {
	d, ok := f[id]
	return d, ok
}

func TestMigracaoInventarioIdempotente(t *testing.T) {
	dir := t.TempDir()
	caminho := filepath.Join(dir, "gameservers.json")
	original := `[{"id":"jogo-b","name":"Enshrouded","game":"enshrouded","container":"jogo-b","root":"/opt/jogo-b","address":""}]`
	if err := os.WriteFile(caminho, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	mudou, err := MigrarInventarioParaNo(caminho)
	if err != nil {
		t.Fatalf("first round: %v", err)
	}
	if !mudou {
		t.Fatal("the first round should have migrated — the record had no such field")
	}
	depois1, err := os.ReadFile(caminho)
	if err != nil {
		t.Fatal(err)
	}

	mudou2, err := MigrarInventarioParaNo(caminho)
	if err != nil {
		t.Fatalf("second round: %v", err)
	}
	if mudou2 {
		t.Error("the second round REWROTE a file that was already correct")
	}
	depois2, err := os.ReadFile(caminho)
	if err != nil {
		t.Fatal(err)
	}
	// Byte for byte, asserted — not "described as idempotent".
	if string(depois1) != string(depois2) {
		t.Errorf("MIGRATION IS NOT IDEMPOTENT:\n--- 1st ---\n%s\n--- 2nd ---\n%s", depois1, depois2)
	}
	if !strings.Contains(string(depois1), `"node"`) {
		t.Errorf("the node field was not added: %s", depois1)
	}
}

func TestMigracaoPreservaCamposDesconhecidos(t *testing.T) {
	dir := t.TempDir()
	caminho := filepath.Join(dir, "gameservers.json")
	// `campoDoFuturo` does not exist in the Server struct. Decoding into []Server
	// and re-serializing would ERASE it — that is the defect this test watches.
	original := `[{"id":"jogo-b","game":"enshrouded","campoDoFuturo":{"a":1,"b":[2,3]},"notes":"nao me apague"}]`
	if err := os.WriteFile(caminho, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrarInventarioParaNo(caminho); err != nil {
		t.Fatal(err)
	}
	depois, err := os.ReadFile(caminho)
	if err != nil {
		t.Fatal(err)
	}
	var registros []map[string]json.RawMessage
	if err := json.Unmarshal(depois, &registros); err != nil {
		t.Fatalf("the migration produced invalid JSON: %v\n%s", err, depois)
	}
	if len(registros) != 1 {
		t.Fatalf("expected 1 record, saw %d", len(registros))
	}
	fut, tem := registros[0]["campoDoFuturo"]
	if !tem {
		t.Fatalf("UNKNOWN FIELD LOST IN THE MIGRATION — silent data loss: %s", depois)
	}
	var v map[string]any
	if err := json.Unmarshal(fut, &v); err != nil {
		t.Fatalf("unknown field corrupted: %v", err)
	}
	if _, ok := v["b"]; !ok {
		t.Errorf("unknown field arrived incomplete: %s", fut)
	}
	if string(registros[0]["notes"]) != `"nao me apague"` {
		t.Errorf("known field altered: %s", registros[0]["notes"])
	}
}

func TestMigracaoGuardaCopiaEArquivoAusenteNaoEErro(t *testing.T) {
	dir := t.TempDir()

	// Absent: a no-op, not an error. The Manager already comes up empty here.
	if mudou, err := MigrarInventarioParaNo(filepath.Join(dir, "nao-existe.json")); err != nil || mudou {
		t.Errorf("a missing file should be a silent no-op, got mudou=%v err=%v", mudou, err)
	}

	caminho := filepath.Join(dir, "gameservers.json")
	original := `[{"id":"x","game":"enshrouded"}]`
	if err := os.WriteFile(caminho, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrarInventarioParaNo(caminho); err != nil {
		t.Fatal(err)
	}
	copia, err := os.ReadFile(caminho + sufixoCopiaPreNo)
	if err != nil {
		t.Fatalf("the copy of the previous file was not kept: %v", err)
	}
	if string(copia) != original {
		t.Errorf("the copy is not the previous content:\n%s", copia)
	}
}

func TestMigracaoNaoEscreveComJSONInvalido(t *testing.T) {
	dir := t.TempDir()
	caminho := filepath.Join(dir, "gameservers.json")
	quebrado := `[{"id":"x",`
	if err := os.WriteFile(caminho, []byte(quebrado), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrarInventarioParaNo(caminho); err == nil {
		t.Error("invalid JSON should abort the migration")
	}
	depois, _ := os.ReadFile(caminho)
	if string(depois) != quebrado {
		t.Errorf("the file was TOUCHED despite the abort: %s", depois)
	}
	if _, err := os.Stat(caminho + sufixoCopiaPreNo); err == nil {
		t.Error("a copy was created even with the migration aborted")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// RESOLUTION

func TestServidorSemNoRecusa(t *testing.T) {
	fonte := fonteFalsa{"games": {Nome: "games", Transport: TransporteAgente, Base: "http://x:1", Token: "t"}}
	s := Server{ID: "jogo-b", Name: "Enshrouded"} // no No

	_, err := ResolverDestino(s, fonte)
	if err == nil {
		t.Fatal("SERVER WITH NO NODE WAS ACCEPTED — the operation would go to a node chosen by omission")
	}
	if !strings.Contains(err.Error(), "jogo-b") {
		t.Errorf("the error has to NAME the server, so the operator knows which one to register: %v", err)
	}
	if !strings.Contains(err.Error(), "node") {
		t.Errorf("the error has to say WHAT to do (register the field): %v", err)
	}
}

func TestResolucaoServidorParaNo(t *testing.T) {
	fonte := fonteFalsa{
		"games": {Nome: "games", Transport: TransporteAgente, Base: "http://x:1", Token: "t"},
		"apps":  {Nome: "apps", Transport: TransportePVEAPI},
	}
	t.Run("existing node", func(t *testing.T) {
		d, err := ResolverDestino(Server{ID: "jogo-b", No: "games"}, fonte)
		if err != nil {
			t.Fatal(err)
		}
		if d.Nome != "games" || d.Transport != TransporteAgente {
			t.Errorf("resolved to the wrong node: %+v", d)
		}
	})
	t.Run("a nonexistent node names the id it looked for", func(t *testing.T) {
		_, err := ResolverDestino(Server{ID: "jogo-b", No: "fantasma"}, fonte)
		if err == nil {
			t.Fatal("a nonexistent node should fail")
		}
		if !strings.Contains(err.Error(), "fantasma") {
			t.Errorf("the error has to name the ID it looked for: %v", err)
		}
	})
	t.Run("no source", func(t *testing.T) {
		if _, err := ResolverDestino(Server{ID: "jogo-b", No: "games"}, nil); err == nil {
			t.Error("a nil source should fail, not fall back to local")
		}
	})
}

func TestNoComTransportInvalido(t *testing.T) {
	casos := map[string]string{
		"valor torto": "banana",
		"vazio":       "",
	}
	for nome, transporte := range casos {
		t.Run(nome, func(t *testing.T) {
			fonte := fonteFalsa{"games": {Nome: "games", Transport: transporte}}
			_, err := ResolverDestino(Server{ID: "jogo-b", No: "games"}, fonte)
			if err == nil {
				t.Fatalf("transport %q should fail instead of picking a back end", transporte)
			}
			if transporte != "" && !strings.Contains(err.Error(), transporte) {
				t.Errorf("the error has to NAME the invalid value: %v", err)
			}
		})
	}
}

// TestResolverNaoTemPadraoImplicito is the control that pins the hard rule: NO
// combination of missing fields may result in a usable destination.
func TestResolverNaoTemPadraoImplicito(t *testing.T) {
	vazios := []struct {
		nome  string
		s     Server
		fonte FonteDeNos
	}{
		{"tudo vazio", Server{}, fonteFalsa{}},
		{"so id", Server{ID: "x"}, fonteFalsa{}},
		{"no vazio com fonte cheia", Server{ID: "x"}, fonteFalsa{"games": {Nome: "games", Transport: TransporteAgente, Base: "http://x:1", Token: "t"}}},
	}
	for _, c := range vazios {
		t.Run(c.nome, func(t *testing.T) {
			d, err := ResolverDestino(c.s, c.fonte)
			if err == nil {
				t.Fatalf("IMPLICIT DEFAULT: resolved to %+v with no node declared", d)
			}
		})
	}
}

// TestSaveInventoryAceitaServidorEmOutroNo — a regression test.
//
// The unconditional `os.Stat(Root)` made it impossible to register a server that
// lives on ANOTHER node: the path exists there, not here. The registration was
// refused with "the folder does not exist" — true about the wrong machine — and
// that blocked the entire multi-node model. The pair below pins BOTH directions,
// because only the positive one would let somebody "simplify" by removing the
// check from the local case as well.
func TestSaveInventoryAceitaServidorEmOutroNo(t *testing.T) {
	dir := t.TempDir()
	// Pre-existing inventory: the atomic write uses the current file as the
	// REFERENCE for owner and mode, so it presupposes the file exists. In a real
	// installation it has existed since the first boot.
	if err := os.WriteFile(filepath.Join(dir, "gameservers.json"), []byte("[]"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := New(dir, nil)

	// With a node: the root belongs to ANOTHER machine and cannot be checked here.
	remoto := []Server{{
		ID: "remoto", Name: "Remoto", Game: "enshrouded",
		Container: "c-remoto", Root: "/opt/nao-existe-neste-host", No: "games",
	}}
	if err := m.SaveInventory(remoto); err != nil {
		t.Fatalf("a server with a node should be accepted (the root lives on the node, not here): %v", err)
	}

	// With no node: it is local, and then a nonexistent root MUST fail — it is the
	// typo caught at registration, and not at the first click on "restart".
	local := []Server{{
		ID: "local", Name: "Local", Game: "enshrouded",
		Container: "c-local", Root: "/opt/tambem-nao-existe", No: "",
	}}
	err := m.SaveInventory(local)
	if err == nil {
		t.Fatal("a server with NO node and a nonexistent root was accepted — the local check disappeared")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("the error does not name the problem: %v", err)
	}
}
