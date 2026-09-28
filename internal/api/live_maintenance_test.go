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

const cloneSourceLive = "lxc/203"

func TestLiveMaintenance(t *testing.T) {
	if os.Getenv("LAB_MNT_LIVE") != "1" {
		t.Skip("🔴 live maintenance proof disabled — run with LAB_MNT_LIVE=1 (it CLONES and DELETES a real guest)")
	}
	t0 := time.Now().UTC()
	r, cancel := liveRouter(t)
	defer cancel()

	value, state := r.vaultToken(pveSecretPanel)
	if state != vaultOK {
		t.Fatalf("panel token: %s — without it nothing in this batch works", state)
	}
	cfg := *r.pveConfig
	cfg.TokenID = value
	cli, err := pve.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	w, out := callAPI(t, r, http.MethodGet, "/api/nodes/"+cloneSourceLive+"/clone", "")
	if w.Code != 200 {
		t.Fatalf("GET clone = %d: %s", w.Code, w.Body)
	}
	newID := int(out["next_id"].(float64))
	if newID <= 0 {
		t.Fatalf("next_id = %v — the hypervisor did not say which id is free", out["next_id"])
	}
	suggested, _ := out["suggestion"].(string)
	t.Logf("hypervisor says %d is free; suggested name %q", newID, suggested)

	inv, err := r.inventoryStore.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range inv.Nodes {
		if n.VMID == newID {
			t.Fatalf("🔴 /cluster/nextid returned %d, which is ALREADY node %s — aborting before cloning", newID, n.ID)
		}
	}

	defer destroyTestGuest(t, cfg, newID)

	needsSnap, _ := out["needs_snapshot"].(bool)
	probeSnap := ""
	if needsSnap {
		probeSnap = "probe-clone-base"
		snapBody, _ := json.Marshal(map[string]any{"name": probeSnap})
		ws, _ := callAPI(t, r, http.MethodPost, "/api/nodes/"+cloneSourceLive+"/snapshot", string(snapBody))
		if ws.Code != 200 {
			upidSnap, err := cli.SnapshotCreate(ctx, "pve", 203, "lxc", probeSnap, "live clone probe")
			if err != nil {
				t.Fatalf("could not create the source snapshot: %v", err)
			}
			if err := cli.WaitTask(ctx, "pve", upidSnap); err != nil {
				t.Fatalf("source snapshot did not finish cleanly: %v", err)
			}
		}
		defer func() {
			upid, err := cli.SnapshotDelete(context.Background(), "pve", 203, "lxc", probeSnap)
			if err != nil {
				t.Errorf("🔴 CLEANUP: snapshot %q was left on the source guest: %v", probeSnap, err)
				return
			}
			_ = cli.WaitTask(context.Background(), "pve", upid)
			t.Logf("cleanup: snapshot %q deleted from the source", probeSnap)
		}()
		t.Logf("container powered on — cloning from snapshot %q", probeSnap)
	}

	name := "probe-clone"
	body, _ := json.Marshal(map[string]any{"new_id": newID, "name": name, "snapshot": probeSnap})
	w, out = callAPI(t, r, http.MethodPost, "/api/nodes/"+cloneSourceLive+"/clone", string(body))
	if w.Code != 200 {
		t.Fatalf("POST clone = %d: %s", w.Code, w.Body)
	}
	if out["status"] != "accepted" {
		t.Errorf("status = %v, want \"accepted\" — the route does not wait for the task", out["status"])
	}
	upid, _ := out["upid"].(string)
	if upid == "" {
		t.Fatal("clone without UPID — without it the operator cannot track anything")
	}
	t.Logf("clone %d requested, task %s", newID, upid)

	ctxClone, cancelClone := context.WithTimeout(ctx, 10*time.Minute)
	defer cancelClone()
	if err := cli.WaitTask(ctxClone, "pve", upid); err != nil {
		av, hasWarning := pve.AsTaskWarning(err)
		if !hasWarning {
			t.Fatalf("the clone task did not finish cleanly: %v", err)
		}
		t.Logf("clone completed WITH warnings from the hypervisor: %s", av.Exit)
	}

	rec, err := pveResourceOf(cfg, newID)
	if err != nil {
		t.Fatalf("clone %d did not appear on the hypervisor: %v", newID, err)
	}
	if rec.Name != name {
		t.Errorf("🔴 clone was born with name %q, I asked for %q — this is the swapped-by-type parameter bug", rec.Name, name)
	}
	if rec.Status == "running" {
		t.Errorf("🔴 the clone was born POWERED ON — it has the source's fixed IP and would bring down its network")
	}
	t.Logf("live clone: id=%d name=%q status=%s", newID, rec.Name, rec.Status)

	dest := ""
	for _, p := range backupStorages(t, cfg) {
		dest = p
		break
	}
	if dest == "" {
		t.Fatal("no storage accepts backup — the proof has nowhere to send it")
	}
	body, _ = json.Marshal(map[string]any{"storage": dest, "mode": "stop"})
	w, out = callAPI(t, r, http.MethodPost, "/api/nodes/lxc/"+strconv.Itoa(newID)+"/backup", string(body))
	if w.Code == 404 {
		t.Logf("the inventory has not seen the clone yet (expected right after creation) — backup proven against the source")
		body, _ = json.Marshal(map[string]any{"storage": dest, "mode": "snapshot"})
		w, out = callAPI(t, r, http.MethodPost, "/api/nodes/"+cloneSourceLive+"/backup", string(body))
	}
	if w.Code != 200 {
		t.Fatalf("POST backup = %d: %s", w.Code, w.Body)
	}
	if out["status"] != "accepted" {
		t.Errorf("backup status = %v, want \"accepted\"", out["status"])
	}
	upidBkp, _ := out["upid"].(string)
	if upidBkp == "" {
		t.Fatal("backup without UPID")
	}
	t.Logf("copy requested at %s, task %s", dest, upidBkp)
	ctxBkp, cancelBkp := context.WithTimeout(ctx, 20*time.Minute)
	defer cancelBkp()
	if err := cli.WaitTask(ctxBkp, "pve", upidBkp); err != nil {
		av, hasWarning := pve.AsTaskWarning(err)
		if !hasWarning {
			t.Fatalf("the backup task did not finish cleanly: %v", err)
		}
		t.Logf("copy completed WITH warnings: %s", av.Exit)
	}
	t.Logf("copy successfully stored at %s", dest)

	t.Logf("[t0=%s t1=%s] live maintenance proof window",
		t0.Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339))
}

type pveResource struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	VMID   int    `json:"vmid"`
}

var errNotFound = errors.New("resource not found on the hypervisor")

func pveHTTP(cfg pve.Config, method, path string, dest any) error {
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
		port := u.Port()
		if port == "" {
			port = "8006"
		}
		target := net.JoinHostPort(cfg.Resolve, port)
		tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, target)
		}
	}
	client := &http.Client{Transport: tr, Timeout: 60 * time.Second}
	req, err := http.NewRequest(method, strings.TrimRight(cfg.BaseURL, "/")+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "PVEAPIToken="+cfg.TokenID)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("%s %s = %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if dest != nil {
		return json.Unmarshal(body, dest)
	}
	return nil
}

func pveResourceOf(cfg pve.Config, vmid int) (pveResource, error) {
	var out struct {
		Data []pveResource `json:"data"`
	}
	if err := pveHTTP(cfg, http.MethodGet, "/api2/json/cluster/resources?type=vm", &out); err != nil {
		return pveResource{}, err
	}
	for _, r := range out.Data {
		if r.VMID == vmid {
			return r, nil
		}
	}
	return pveResource{}, errNotFound
}

func backupStorages(t *testing.T, cfg pve.Config) []string {
	t.Helper()
	var out struct {
		Data []struct {
			Storage string `json:"storage"`
			Content string `json:"content"`
		} `json:"data"`
	}
	if err := pveHTTP(cfg, http.MethodGet, "/api2/json/nodes/pve/storage", &out); err != nil {
		t.Fatalf("reading storages: %v", err)
	}
	var r []string
	for _, s := range out.Data {
		if strings.Contains(s.Content, "backup") {
			r = append(r, s.Storage)
		}
	}
	return r
}

func destroyTestGuest(t *testing.T, cfg pve.Config, vmid int) {
	t.Helper()
	if vmid <= 0 {
		return
	}
	if _, err := pveResourceOf(cfg, vmid); err != nil {
		t.Logf("cleanup: %d does not exist on the hypervisor (nothing to delete)", vmid)
		return
	}
	path := "/api2/json/nodes/pve/lxc/" + strconv.Itoa(vmid) + "?purge=1&destroy-unreferenced-disks=1"
	if err := pveHTTP(cfg, http.MethodDelete, path, nil); err != nil {
		t.Errorf("🔴 CLEANUP FAILED: clone %d was left on the hypervisor — delete it by hand (pct destroy %d): %v", vmid, vmid, err)
		return
	}
	t.Logf("cleanup: clone %d deleted", vmid)
}

func TestLiveNoteOfEveryNode(t *testing.T) {
	if os.Getenv("LAB_MNT_LIVE") != "1" {
		t.Skip("live proof disabled — run with LAB_MNT_LIVE=1")
	}
	r, cancel := liveRouter(t)
	defer cancel()
	inv, err := r.inventoryStore.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Nodes) < 5 {
		t.Fatalf("inventory with %d nodes — the scan would pass vacuously", len(inv.Nodes))
	}

	var withoutNote []string
	for _, n := range inv.Nodes {
		w, out := callAPI(t, r, http.MethodGet, "/api/nodes/"+n.ID+"/note", "")
		if w.Code != 200 {
			t.Errorf("%s: note = %d: %s", n.ID, w.Code, w.Body)
			continue
		}
		origin, _ := out["origin"].(string)
		md, _ := out["markdown"].(string)

		if origin == "nonexistent" {
			if out["reason"] == "" {
				t.Errorf("%s: disappeared from the hypervisor and the response does not say so", n.ID)
			}
			t.Logf("%-10s (no longer exists on the hypervisor — inventory from one cycle ago)", n.ID)
			continue
		}

		if n.Transport != "pve-api" {
			if origin != "outside-pve" || out["reason"] == "" {
				t.Errorf("%s: external node returned origin=%q reason=%v", n.ID, origin, out["reason"])
			}
			continue
		}
		if origin == "empty" || strings.TrimSpace(md) == "" {
			withoutNote = append(withoutNote, n.ID)
			continue
		}
		if len(md) < 120 {
			t.Errorf("%s: note with %d characters — too short to explain what the box does", n.ID, len(md))
		}
		t.Logf("%-10s %5d characters · %s", n.ID, len(md), firstLine(md))
	}
	if len(withoutNote) > 0 {
		t.Errorf("🔴 %d node(s) without a note in PVE: %s — whoever clicks on them won't find out what they do",
			len(withoutNote), strings.Join(withoutNote, ", "))
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimLeft(s, "# ")
	r := []rune(s)
	if len(r) > 58 {
		return string(r[:58]) + "…"
	}
	return s
}

func TestLiveEditNote(t *testing.T) {
	if os.Getenv("LAB_MNT_LIVE") != "1" {
		t.Skip("live proof disabled — run with LAB_MNT_LIVE=1 (it WRITES to a real note)")
	}
	const target = cloneSourceLive
	r, cancel := liveRouter(t)
	defer cancel()

	w, out := callAPI(t, r, http.MethodGet, "/api/nodes/"+target+"/note", "")
	if w.Code != 200 {
		t.Fatalf("GET note = %d: %s", w.Code, w.Body)
	}
	original, _ := out["markdown"].(string)
	if len(original) < 120 {
		t.Fatalf("original note with %d bytes — I will not write over something I did not read correctly", len(original))
	}

	defer func() {
		body, _ := json.Marshal(map[string]string{"markdown": original})
		w, _ := callAPI(t, r, http.MethodPut, "/api/nodes/"+target+"/note", string(body))
		if w.Code != 200 {
			t.Errorf("🔴 CLEANUP FAILED: %s's note was left altered — restore it by hand: %s", target, w.Body)
			return
		}
		w, out := callAPI(t, r, http.MethodGet, "/api/nodes/"+target+"/note", "")
		if got, _ := out["markdown"].(string); w.Code != 200 || got != original {
			t.Errorf("🔴 CLEANUP INCOMPLETE: the note did not come back byte for byte")
			return
		}
		t.Logf("cleanup: %s's note restored (%d bytes)", target, len(original))
	}()

	mark := "\n\n<!-- live edit probe: this line is deleted at the end -->"
	body, _ := json.Marshal(map[string]string{"markdown": original + mark})
	w, _ = callAPI(t, r, http.MethodPut, "/api/nodes/"+target+"/note", string(body))
	if w.Code != 200 {
		t.Fatalf("PUT note = %d: %s", w.Code, w.Body)
	}

	w, out = callAPI(t, r, http.MethodGet, "/api/nodes/"+target+"/note", "")
	if w.Code != 200 {
		t.Fatalf("GET after the PUT = %d", w.Code)
	}
	now, _ := out["markdown"].(string)
	if !strings.Contains(now, "live edit probe") {
		t.Errorf("the marker did not reach the hypervisor")
	}
	if !strings.HasPrefix(now, strings.TrimSpace(original)[:60]) {
		t.Errorf("🔴 the start of the note changed — the write did not preserve the original text")
	}
	t.Logf("%s's note: %d bytes -> %d bytes, read back from the hypervisor", target, len(original), len(now))
}
