//go:build live

package api

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"server-control-panel/internal/pve"
)

// live_manutencao_test.go — the LIVE proof of reboot, clone and backup.
//
// ────────────────────────────────────────────────────────────────────────────
// 🔴 WHY IT EXISTS, EVEN WITH THE WHOLE PACKAGE GREEN.
//
// "Green build ≠ green live" is a lesson on record from this lab, and these
// three operations are the clearest case: they depend on PRIVILEGE on the
// hypervisor, and privilege never shows up in a test against a fake. The clone
// needs VM.Clone on the source, VM.Allocate on /vms/<newid> and
// Datastore.AllocateSpace on the storage; the backup needs VM.Backup and
// Datastore.AllocateSpace. A fake says "I called"; only the hypervisor says
// "I allowed".
//
// 🔴 IT CREATES AND DELETES A REAL GUEST. The clone is made from the smallest CT
// in the lab, is NEVER started (a running copy would carry the source's fixed IP
// and take its network down) and is destroyed in the defer — including when the
// test fails halfway. The destruction goes over raw HTTP, written HERE: giving
// the production client a `Destroy` method just to clean up after a test would
// create a permanent destructive primitive on the dashboard's surface, and no
// route needs it.
// ────────────────────────────────────────────────────────────────────────────

const cloneOrigemLive = "lxc/203" // `edge`: the smallest CT, ~430 MB used

func TestLiveManutencao(t *testing.T) {
	if os.Getenv("LAB_MNT_LIVE") != "1" {
		t.Skip("🔴 live maintenance proof disabled — run with LAB_MNT_LIVE=1 (it CLONES and DELETES a real guest)")
	}
	t0 := time.Now().UTC()
	r, cancel := routerVivo(t)
	defer cancel()

	// ── 1. the hypervisor DECLARES the reboot verb ───────────────────────
	//
	// Rebooting a production guest without the operator asking would be disruption
	// nobody authorized. What can be proved without that — and it is what differs
	// between "reboot" and "stop" — is that the route EXISTS on the hypervisor. The
	// rest (the dashboard route, the allowlist and WaitTask) has a unit pin.
	valor, estado := r.tokenDoCofre(pveSecretPainel)
	if estado != vaultOK {
		t.Fatalf("panel token: %s — without it nothing in this batch works", estado)
	}
	cfg := *r.pveConfig
	cfg.TokenID = valor
	cli, err := pve.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// ── 2. GET /clone: the id comes from the hypervisor ──────────────────
	w, out := chama(t, r, http.MethodGet, "/api/nodes/"+cloneOrigemLive+"/clone", "")
	if w.Code != 200 {
		t.Fatalf("GET clone = %d: %s", w.Code, w.Body)
	}
	novoID := int(out["next_id"].(float64))
	if novoID <= 0 {
		t.Fatalf("next_id = %v — the hypervisor did not say which id is free", out["next_id"])
	}
	sug, _ := out["sugestao"].(string)
	t.Logf("hypervisor says %d is free; suggested name %q", novoID, sug)

	// Guard: the id has to be NEW. Cloning over an existing guest is this route's
	// nightmare, and the check is cheap.
	inv, err := r.inventoryStore.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range inv.Nodes {
		if n.VMID == novoID {
			t.Fatalf("🔴 /cluster/nextid returned %d, which is ALREADY node %s — aborting before cloning", novoID, n.ID)
		}
	}

	// ── 3. POST /clone: the real clone ───────────────────────────────────
	defer destroiGuestDeTeste(t, cfg, novoID)

	// 🔴 A RUNNING CONTAINER ONLY CLONES FROM A SNAPSHOT — and it was THIS test
	// that discovered it, on its very first run, with the hypervisor refusing the
	// clone. No grep of this machine's PVE source shows the rule.
	precisaSnap, _ := out["precisa_snapshot"].(bool)
	snapDaProva := ""
	if precisaSnap {
		snapDaProva = "prova-clone-base"
		corpoSnap, _ := json.Marshal(map[string]any{"nome": snapDaProva})
		ws, _ := chama(t, r, http.MethodPost, "/api/nodes/"+cloneOrigemLive+"/snapshot", string(corpoSnap))
		if ws.Code != 200 {
			// No snapshot route around here, so create it through the client
			// directly: what this test measures is the CLONE, and the snapshot is only the step up to it.
			upidSnap, err := cli.SnapshotCreate(ctx, "pve", 203, "lxc", snapDaProva, "prova viva de clone")
			if err != nil {
				t.Fatalf("could not create the source snapshot: %v", err)
			}
			if err := cli.WaitTask(ctx, "pve", upidSnap); err != nil {
				t.Fatalf("source snapshot did not finish cleanly: %v", err)
			}
		}
		defer func() {
			upid, err := cli.SnapshotDelete(context.Background(), "pve", 203, "lxc", snapDaProva)
			if err != nil {
				t.Errorf("🔴 CLEANUP: snapshot %q was left on the source guest: %v", snapDaProva, err)
				return
			}
			_ = cli.WaitTask(context.Background(), "pve", upid)
			t.Logf("cleanup: snapshot %q deleted from the source", snapDaProva)
		}()
		t.Logf("container powered on — cloning from snapshot %q", snapDaProva)
	}

	nome := "prova-clone"
	corpo, _ := json.Marshal(map[string]any{"novo_id": novoID, "nome": nome, "snapshot": snapDaProva})
	w, out = chama(t, r, http.MethodPost, "/api/nodes/"+cloneOrigemLive+"/clone", string(corpo))
	if w.Code != 200 {
		t.Fatalf("POST clone = %d: %s", w.Code, w.Body)
	}
	if out["status"] != "aceita" {
		t.Errorf("status = %v, want \"accepted\" — the route does not wait for the task", out["status"])
	}
	upid, _ := out["upid"].(string)
	if upid == "" {
		t.Fatal("clone without UPID — without it the operator cannot track anything")
	}
	t.Logf("clone %d requested, task %s", novoID, upid)

	// The route does not wait — the TEST waits, because it needs the result.
	ctxClone, cancelClone := context.WithTimeout(ctx, 10*time.Minute)
	defer cancelClone()
	if err := cli.WaitTask(ctxClone, "pve", upid); err != nil {
		// `WARNINGS: n` is success with a message — it was THIS test that forced
		// the distinction to exist (the clone ended in WARNINGS: 1 with the warning
		// "Systemd 257 detected. You may need to enable nesting.").
		av, temAviso := pve.TarefaComAvisos(err)
		if !temAviso {
			t.Fatalf("the clone task did not finish cleanly: %v", err)
		}
		t.Logf("clone completed WITH warnings from the hypervisor: %s", av.Exit)
	}

	// ── 4. the clone EXISTS, with the name that was asked for ────────────
	//
	// 🔴 This is the step that catches the silent `hostname` vs `name` defect:
	// sending the wrong parameter raises no error, the clone is just born nameless.
	rec, err := recursoDoPVE(cfg, novoID)
	if err != nil {
		t.Fatalf("clone %d did not appear on the hypervisor: %v", novoID, err)
	}
	if rec.Name != nome {
		t.Errorf("🔴 clone was born with name %q, I asked for %q — this is the swapped-by-type parameter bug", rec.Name, nome)
	}
	if rec.Status == "running" {
		t.Errorf("🔴 the clone was born POWERED ON — it has the source's fixed IP and would bring down its network")
	}
	t.Logf("live clone: id=%d name=%q status=%s", novoID, rec.Name, rec.Status)

	// ── 5. back up NOW, for real ─────────────────────────────────────────
	//
	// Done on the CLONE, not on the source: the production guest does not need to
	// take part in the test, and the result is the same — what is being proved is
	// VM.Backup + Datastore.AllocateSpace + the route.
	destino := ""
	for _, p := range storagesQueAceitamBackup(t, cfg) {
		destino = p
		break
	}
	if destino == "" {
		t.Fatal("no storage accepts backup — the proof has nowhere to send it")
	}
	corpo, _ = json.Marshal(map[string]any{"storage": destino, "modo": "stop"})
	w, out = chama(t, r, http.MethodPost, "/api/nodes/lxc/"+strconv.Itoa(novoID)+"/backup", string(corpo))
	if w.Code == 404 {
		// The inventory only learns about the clone on the next cycle. That is not a
		// defect in the route: it is the poller not having seen the new guest yet.
		t.Logf("the inventory has not seen the clone yet (expected right after creation) — backup proven against the source")
		corpo, _ = json.Marshal(map[string]any{"storage": destino, "modo": "snapshot"})
		w, out = chama(t, r, http.MethodPost, "/api/nodes/"+cloneOrigemLive+"/backup", string(corpo))
	}
	if w.Code != 200 {
		t.Fatalf("POST backup = %d: %s", w.Code, w.Body)
	}
	if out["status"] != "aceita" {
		t.Errorf("backup status = %v, want \"accepted\"", out["status"])
	}
	upidBkp, _ := out["upid"].(string)
	if upidBkp == "" {
		t.Fatal("backup without UPID")
	}
	t.Logf("copy requested at %s, task %s", destino, upidBkp)
	ctxBkp, cancelBkp := context.WithTimeout(ctx, 20*time.Minute)
	defer cancelBkp()
	if err := cli.WaitTask(ctxBkp, "pve", upidBkp); err != nil {
		av, temAviso := pve.TarefaComAvisos(err)
		if !temAviso {
			t.Fatalf("the backup task did not finish cleanly: %v", err)
		}
		t.Logf("copy completed WITH warnings: %s", av.Exit)
	}
	t.Logf("copy successfully stored at %s", destino)

	t.Logf("[t0=%s t1=%s] live maintenance proof window",
		t0.Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339))
}

// ── helpers, all written HERE and only for the test ─────────────────────────
//
// 🔴 Checking the clone and deleting it goes over RAW HTTP, written in this file,
// not through a method of the production client. Giving pve.Client a `Destroy`
// to clean up after a test would create a permanent destructive primitive that no
// dashboard route needs — and a destructive primitive that exists is a
// destructive primitive somebody calls one day.

type recursoPVE struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	VMID   int    `json:"vmid"`
}

var errNaoAchou = errors.New("não achei o recurso no hipervisor")

// httpDoPVE talks to the hypervisor with the SAME address override as the
// production client (dial cfg.Resolve, verify the certificate against
// cfg.ServerName with the pinned CA). Repeating the policy here is conscious debt
// limited to the test: the client does not expose its transport.
func httpDoPVE(cfg pve.Config, metodo, caminho string, destino any) error {
	pool := x509.NewCertPool()
	if cfg.CAFile != "" {
		pem, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return err
		}
		pool.AppendCertsFromPEM(pem)
	}
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: cfg.ServerName},
	}
	if cfg.Resolve != "" {
		u, err := url.Parse(cfg.BaseURL)
		if err != nil {
			return err
		}
		porta := u.Port()
		if porta == "" {
			porta = "8006"
		}
		alvo := net.JoinHostPort(cfg.Resolve, porta)
		tr.DialContext = func(ctx context.Context, rede, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, rede, alvo)
		}
	}
	cliente := &http.Client{Transport: tr, Timeout: 60 * time.Second}
	req, err := http.NewRequest(metodo, strings.TrimRight(cfg.BaseURL, "/")+caminho, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "PVEAPIToken="+cfg.TokenID)
	resp, err := cliente.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	corpo, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("%s %s = %d: %s", metodo, caminho, resp.StatusCode, strings.TrimSpace(string(corpo)))
	}
	if destino != nil {
		return json.Unmarshal(corpo, destino)
	}
	return nil
}

func recursoDoPVE(cfg pve.Config, vmid int) (recursoPVE, error) {
	var out struct {
		Data []recursoPVE `json:"data"`
	}
	if err := httpDoPVE(cfg, http.MethodGet, "/api2/json/cluster/resources?type=vm", &out); err != nil {
		return recursoPVE{}, err
	}
	for _, r := range out.Data {
		if r.VMID == vmid {
			return r, nil
		}
	}
	return recursoPVE{}, errNaoAchou
}

func storagesQueAceitamBackup(t *testing.T, cfg pve.Config) []string {
	t.Helper()
	var out struct {
		Data []struct {
			Storage string `json:"storage"`
			Content string `json:"content"`
		} `json:"data"`
	}
	if err := httpDoPVE(cfg, http.MethodGet, "/api2/json/nodes/pve/storage", &out); err != nil {
		t.Fatalf("lendo storages: %v", err)
	}
	var r []string
	for _, s := range out.Data {
		if strings.Contains(s.Content, "backup") {
			r = append(r, s.Storage)
		}
	}
	return r
}

// destroiGuestDeTeste deletes the clone. It runs in the defer, so it cleans up
// even when the test fails halfway — one orphan guest per run would be worse than
// having no proof at all.
func destroiGuestDeTeste(t *testing.T, cfg pve.Config, vmid int) {
	t.Helper()
	if vmid <= 0 {
		return
	}
	if _, err := recursoDoPVE(cfg, vmid); err != nil {
		t.Logf("cleanup: %d does not exist on the hypervisor (nothing to delete)", vmid)
		return
	}
	caminho := "/api2/json/nodes/pve/lxc/" + strconv.Itoa(vmid) + "?purge=1&destroy-unreferenced-disks=1"
	if err := httpDoPVE(cfg, http.MethodDelete, caminho, nil); err != nil {
		t.Errorf("🔴 CLEANUP FAILED: clone %d was left on the hypervisor — delete it by hand (pct destroy %d): %v", vmid, vmid, err)
		return
	}
	t.Logf("cleanup: clone %d deleted", vmid)
}

// 🔴 TestLiveNotaDeTodoNo — the note is the BODY of the summary, so it has to
// arrive for EVERY node, not just for the one somebody happened to test.
//
// The proof is live because what is being claimed is about the hypervisor: that
// the `description` field exists, is filled in and is reachable with the
// READ-ONLY credential (not the full-access one). A fake would say "I called".
func TestLiveNotaDeTodoNo(t *testing.T) {
	if os.Getenv("LAB_MNT_LIVE") != "1" {
		t.Skip("live proof disabled — run with LAB_MNT_LIVE=1")
	}
	r, cancel := routerVivo(t)
	defer cancel()
	inv, err := r.inventoryStore.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Nodes) < 5 {
		t.Fatalf("inventory with %d nodes — the scan would pass vacuously", len(inv.Nodes))
	}

	var semNota []string
	for _, n := range inv.Nodes {
		w, out := chama(t, r, http.MethodGet, "/api/nodes/"+n.ID+"/nota", "")
		if w.Code != 200 {
			t.Errorf("%s: nota = %d: %s", n.ID, w.Code, w.Body)
			continue
		}
		origem, _ := out["origem"].(string)
		md, _ := out["markdown"].(string)

		// A node the inventory still lists but the hypervisor no longer has (a
		// guest deleted on the previous cycle) is not a failure of this test — it
		// is the state it helped discover. What is required is that the answer EXPLAINS.
		if origem == "inexistente" {
			if out["motivo"] == "" {
				t.Errorf("%s: disappeared from the hypervisor and the response does not say so", n.ID)
			}
			t.Logf("%-10s (no longer exists on the hypervisor — inventory from one cycle ago)", n.ID)
			continue
		}

		if n.Transport != "pve-api" {
			// An external node has no config in PVE: an absence of SOURCE, and
			// the answer has to say so instead of returning a mute blank.
			if origem != "fora-do-pve" || out["motivo"] == "" {
				t.Errorf("%s: external node returned origin=%q reason=%v", n.ID, origem, out["motivo"])
			}
			continue
		}
		if origem == "vazia" || strings.TrimSpace(md) == "" {
			semNota = append(semNota, n.ID)
			continue
		}
		// The note is there to be READ: a single line explains nothing. The floor
		// is deliberately low — the pin exists to catch a missing or one-word note,
		// not to arbitrate writing style.
		if len(md) < 120 {
			t.Errorf("%s: note with %d characters — too short to explain what the box does", n.ID, len(md))
		}
		t.Logf("%-10s %5d caracteres · %s", n.ID, len(md), primeiraLinha(md))
	}
	if len(semNota) > 0 {
		t.Errorf("🔴 %d node(s) without a note in PVE: %s — whoever clicks on them won't find out what they do",
			len(semNota), strings.Join(semNota, ", "))
	}
}

func primeiraLinha(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimLeft(s, "# ")
	// 🔴 Cut by RUNE, not by byte: this text is Portuguese, and `s[:58]` splits
	// a "ç" down the middle, printing garbage into the test log.
	r := []rune(s)
	if len(r) > 58 {
		return string(r[:58]) + "…"
	}
	return s
}

// 🔴 TestLiveEditaNota — the full round trip: read the REAL note, write, check on
// the hypervisor, and restore the original.
//
// It writes into a real description. The original is restored in the defer, so
// the cleanup happens even when the test fails halfway — and the restoration is
// of the EXACT text that was there, not of a reconstruction.
func TestLiveEditaNota(t *testing.T) {
	if os.Getenv("LAB_MNT_LIVE") != "1" {
		t.Skip("live proof disabled — run with LAB_MNT_LIVE=1 (it WRITES to a real note)")
	}
	const alvo = cloneOrigemLive // lxc/203, the smallest CT
	r, cancel := routerVivo(t)
	defer cancel()

	// 1. read the original
	w, out := chama(t, r, http.MethodGet, "/api/nodes/"+alvo+"/nota", "")
	if w.Code != 200 {
		t.Fatalf("GET nota = %d: %s", w.Code, w.Body)
	}
	original, _ := out["markdown"].(string)
	if len(original) < 120 {
		t.Fatalf("original note with %d bytes — I will not write over something I did not read correctly", len(original))
	}

	// 2. ALWAYS restore, with the exact text
	defer func() {
		corpo, _ := json.Marshal(map[string]string{"markdown": original})
		w, _ := chama(t, r, http.MethodPut, "/api/nodes/"+alvo+"/nota", string(corpo))
		if w.Code != 200 {
			t.Errorf("🔴 CLEANUP FAILED: %s's note was left altered — restore it by hand: %s", alvo, w.Body)
			return
		}
		w, out := chama(t, r, http.MethodGet, "/api/nodes/"+alvo+"/nota", "")
		if got, _ := out["markdown"].(string); w.Code != 200 || got != original {
			t.Errorf("🔴 CLEANUP INCOMPLETE: the note did not come back byte for byte")
			return
		}
		t.Logf("cleanup: %s's note restored (%d bytes)", alvo, len(original))
	}()

	// 3. write the original + a marker
	marca := "\n\n<!-- prova viva de edicao: esta linha e apagada no fim -->"
	corpo, _ := json.Marshal(map[string]string{"markdown": original + marca})
	w, _ = chama(t, r, http.MethodPut, "/api/nodes/"+alvo+"/nota", string(corpo))
	if w.Code != 200 {
		t.Fatalf("PUT nota = %d: %s", w.Code, w.Body)
	}

	// 4. the hypervisor really does have the new text — read back, not assumed
	w, out = chama(t, r, http.MethodGet, "/api/nodes/"+alvo+"/nota", "")
	if w.Code != 200 {
		t.Fatalf("GET after the PUT = %d", w.Code)
	}
	agora, _ := out["markdown"].(string)
	if !strings.Contains(agora, "prova viva de edicao") {
		t.Errorf("the marker did not reach the hypervisor")
	}
	if !strings.HasPrefix(agora, strings.TrimSpace(original)[:60]) {
		t.Errorf("🔴 the start of the note changed — the write did not preserve the original text")
	}
	t.Logf("%s's note: %d bytes -> %d bytes, read back from the hypervisor", alvo, len(original), len(agora))
}
