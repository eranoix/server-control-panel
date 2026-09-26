//go:build live

package api

// live_sto_test.go — the LIVE proof of pool topology against the home
// hypervisor. Gated by LAB_STO_LIVE=1.
//
// It exercises the ROUTE, not the client: that way it proves the whole plumbing
// (permission, serialization, JSON shape) and not just decoding the tree.

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLiveTopologiaDosPools(t *testing.T) {
	if os.Getenv("LAB_STO_LIVE") != "1" {
		t.Skip("live proof disabled — run with LAB_STO_LIVE=1")
	}
	r, cancel := routerVivo(t)
	defer cancel()

	w, out := pvxGET(t, r, "/api/proxmox/zfs/topologia")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/proxmox/zfs/topologia = %d: %s", w.Code, w.Body.String())
	}
	pools, _ := out["pools"].([]any)
	if len(pools) == 0 {
		t.Fatal("no pool — without coverage, this test proves nothing")
	}
	for _, p := range pools {
		m, _ := p.(map[string]any)
		nome, _ := m["nome"].(string)
		estado, _ := m["estado"].(string)
		red, _ := m["redundante"].(bool)
		nDisp, _ := m["n_dispositivos"].(float64)
		erros, _ := m["erros_contados"].(float64)

		if nDisp == 0 {
			t.Errorf("pool %s: no device read — the tree was not decoded", nome)
		}
		// 🔴 Negative control: a single-disk pool must NOT come out redundant.
		// It is the assertion that stops the screen promising protection that does not exist.
		if nDisp == 1 && red {
			t.Errorf("pool %s has 1 device and came out as redundant", nome)
		}
		t.Logf("%-8s %-9s redundant=%-5v %.0f avail · errors=%.0f · %v",
			nome, estado, red, nDisp, erros, m["erros"])

		vdevs, _ := m["vdevs"].([]any)
		for _, v := range vdevs {
			vm, _ := v.(map[string]any)
			disps, _ := vm["dispositivos"].([]any)
			for _, d := range disps {
				dm, _ := d.(map[string]any)
				t.Logf("           %v [%v] r=%v w=%v ck=%v",
					dm["caminho"], dm["estado"], dm["read"], dm["write"], dm["cksum"])
			}
		}
	}
}

func TestLiveFrescorDeBackup(t *testing.T) {
	if os.Getenv("LAB_STO_LIVE") != "1" {
		t.Skip("live proof disabled — run with LAB_STO_LIVE=1")
	}
	r, cancel := routerVivo(t)
	defer cancel()

	w, out := pvxGET(t, r, "/api/proxmox/backup")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/proxmox/backup = %d: %s", w.Code, w.Body.String())
	}
	ds, _ := out["datastores"].([]any)
	if len(ds) == 0 {
		t.Fatal("no backup datastore — without coverage, this test proves nothing")
	}
	var comCopia int
	for _, d := range ds {
		m, _ := d.(map[string]any)
		st, _ := m["storage"].(string)
		total, _ := m["total"].(float64)
		ultimo, _ := m["ultimo_ctime"].(float64)
		guests, _ := m["guests"].([]any)
		if e, _ := m["erro"].(string); e != "" {
			t.Logf("%-12s ERROR: %s", st, e)
			continue
		}
		if total > 0 {
			comCopia++
			// 🔴 If there is a backup, there MUST be a timestamp. `ultimo_ctime`
			// at zero with total>0 would be the screen saying "there is a backup,
			// from 1970" — worse than saying it does not know.
			if ultimo == 0 {
				t.Errorf("%s: %d backups and ultimo_ctime=0 — timestamp lost", st, int(total))
			}
			t.Logf("%-12s %3d copies · %d guests · last %.1f h ago",
				st, int(total), len(guests), time.Since(time.Unix(int64(ultimo), 0)).Hours())
		} else {
			t.Logf("%-12s empty (0 copies) — absence, not '1970'", st)
		}
	}
	if comCopia == 0 {
		t.Error("no datastore with a copy — either the lab has no backup, or the read is broken")
	}
}

// TestLiveParidadeComProxmox exercises the routes that give parity with the
// Proxmox screen. None of them answered before full access was granted.
func TestLiveParidadeComProxmox(t *testing.T) {
	if os.Getenv("LAB_STO_LIVE") != "1" {
		t.Skip("live proof disabled — run with LAB_STO_LIVE=1")
	}
	r, cancel := routerVivo(t)
	defer cancel()

	t.Run("node series", func(t *testing.T) {
		w, out := pvxGET(t, r, "/api/proxmox/rrd?janela=hour")
		if w.Code != http.StatusOK {
			t.Fatalf("= %d: %s", w.Code, w.Body.String())
		}
		pts, _ := out["pontos"].([]any)
		if len(pts) < 10 {
			t.Fatalf("only %d points — without coverage, the graph proves nothing", len(pts))
		}
		ult, _ := pts[len(pts)-1].(map[string]any)
		t.Logf("%d points · last: cpu=%v iowait=%v load=%v arc=%v",
			len(pts), ult["cpu"], ult["iowait"], ult["loadavg"], ult["arcsize"])
	})

	t.Run("invalid window falls back to the default, does not become a path", func(t *testing.T) {
		w, out := pvxGET(t, r, "/api/proxmox/rrd?janela=../../access/users")
		if w.Code != http.StatusOK {
			t.Fatalf("= %d", w.Code)
		}
		if j, _ := out["janela"].(string); j != "hour" {
			t.Errorf("janela = %q, expected the default 'hour' — the allowlist did not hold", j)
		}
	})

	t.Run("series for a single guest", func(t *testing.T) {
		w, out := pvxGET(t, r, "/api/proxmox/rrd?node=qemu/208&janela=hour")
		if w.Code != http.StatusOK {
			t.Fatalf("= %d: %s", w.Code, w.Body.String())
		}
		pts, _ := out["pontos"].([]any)
		if len(pts) < 10 {
			t.Fatalf("only %d points for the guest", len(pts))
		}
		t.Logf("qemu/208: %d pontos", len(pts))
	})

	t.Run("sistema", func(t *testing.T) {
		w, out := pvxGET(t, r, "/api/proxmox/sistema")
		if w.Code != http.StatusOK {
			t.Fatalf("= %d: %s", w.Code, w.Body.String())
		}
		for _, k := range []string{"network", "dns", "time", "certificados"} {
			if _, ok := out[k]; !ok {
				t.Errorf("block %q missing (error: %v)", k, out[k+"_erro"])
			}
		}
		ifaces, _ := out["network"].([]any)
		tm, _ := out["time"].(map[string]any)
		certs, _ := out["certificados"].([]any)
		t.Logf("%d interfaces · fuso=%v · %d certificados", len(ifaces), tm["timezone"], len(certs))
	})

	t.Run("pacotes", func(t *testing.T) {
		w, out := pvxGET(t, r, "/api/proxmox/pacotes")
		if w.Code != http.StatusOK {
			t.Fatalf("= %d: %s", w.Code, w.Body.String())
		}
		ps, _ := out["pacotes"].([]any)
		if len(ps) == 0 {
			t.Fatal("no package — the route responded empty")
		}
		t.Logf("%d pacotes instalados", len(ps))
	})

	t.Run("syslog", func(t *testing.T) {
		w, out := pvxGET(t, r, "/api/proxmox/syslog?limit=5")
		if w.Code != http.StatusOK {
			t.Fatalf("= %d: %s", w.Code, w.Body.String())
		}
		ls, _ := out["linhas"].([]any)
		if len(ls) == 0 {
			t.Fatal("empty syslog — the route responded without a single line")
		}
		t.Logf("%d lines (requested limit 5)", len(ls))
	})
}

// TestLiveShellDoHipervisor proves the COMPLETE handshake of the host shell —
// termproxy, the upgrade to WebSocket and the auth frame with its "OK".
//
// 🔴 It only exists since full access was granted: POST /nodes/{n}/termproxy
// requires Sys.Console, and the audit token used to get a 403.
func TestLiveShellDoHipervisor(t *testing.T) {
	if os.Getenv("LAB_STO_LIVE") != "1" {
		t.Skip("live proof disabled — run with LAB_STO_LIVE=1")
	}
	r, cancel := routerVivo(t)
	defer cancel()

	valor, estado := r.tokenDoCofre(r.segredoDeLeituraDoHipervisor())
	if estado != vaultOK {
		t.Fatalf("hypervisor read token: %s", estado)
	}
	cli, err := r.dial(valor)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	ctx, c2 := context.WithTimeout(context.Background(), 25*time.Second)
	defer c2()

	conn, upid, err := cli.ConsoleAttachNode(ctx, "pve")
	if err != nil {
		t.Fatalf("ConsoleAttachNode: %v", err)
	}
	defer conn.Close()
	if upid == "" {
		t.Error("no UPID — the console task was not registered on the hypervisor")
	}
	// The UPID says `vncshell` for the node and `vncproxy` for a guest: that is the
	// tie-breaker proving the path taken was the HOST's, not a guest's by mistake.
	if !strings.Contains(upid, "vncshell") {
		t.Errorf("UPID = %q, expected to contain 'vncshell' (the node's path)", upid)
	}
	t.Logf("hypervisor shell opened · %s", upid)
}

// TestLiveDesarmadoNaoEFalha proves the distinction that was missing: a datastore
// with no fresh backup for two weeks because the SCHEDULE was turned off is not
// the same as one that failed. Permanent red trains people to ignore.
func TestLiveDesarmadoNaoEFalha(t *testing.T) {
	if os.Getenv("LAB_STO_LIVE") != "1" {
		t.Skip("live proof disabled — run with LAB_STO_LIVE=1")
	}
	r, cancel := routerVivo(t)
	defer cancel()
	_, out := pvxGET(t, r, "/api/proxmox/backup")
	ds, _ := out["datastores"].([]any)
	if len(ds) == 0 {
		t.Fatal("no datastore — without coverage")
	}
	var comAgenda int
	for _, d := range ds {
		m, _ := d.(map[string]any)
		st, _ := m["storage"].(string)
		ag, _ := m["agendamento"].(string)
		sch, _ := m["schedule"].(string)
		if ag == "ativo" {
			comAgenda++
		}
		t.Logf("%-12s agendamento=%-12s schedule=%q", st, ag, sch)
	}
	t.Logf("%d of %d datastores have a live schedule", comAgenda, len(ds))
}
