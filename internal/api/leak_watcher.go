package api

// leak_watcher.go — the tunnel's LEAK sentinel.
//
// The concrete fear of anyone routing their phone through home: believing the
// traffic leaves through the house when, thanks to the gateway going down or a
// silent fallback, it has started leaving through the VPS itself. "It looks
// protected" is worse than knowing it has failed. This sentinel runs on the VPS
// and, on every cycle, proves through the house's SOCKS what the real egress IP
// is — and compares it with the VPS's IP.
//
// It alerts ON THE EDGE (like hypervisor_watcher): only the transition produces
// an event, and it waits N cycles before believing it (networks wobble). Two
// symptoms:
//   - the house egress unreachable for N cycles → the gateway is down / the
//     route is broken.
//   - the house egress leaving via the SAME IP as the VPS → A LEAK: what should
//     be going through the house is going through the VPS.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"server-control-panel/internal/notify"

	"golang.org/x/net/proxy"
)

const (
	leakInterval   = 5 * time.Minute
	leakFailBefore = 3 // ciclos consecutivos antes de acreditar na queda
	leakDedup      = "tunnel:leak"
	TipoTunnelLeak = "tunnel.leak"      // casa saindo pelo VPS
	TipoTunnelDown = "tunnel.casa_down" // home exit unreachable
	TipoTunnelOK   = "tunnel.recovered" // voltou ao normal
)

type leakSentinela struct {
	mu       sync.Mutex
	fails    int
	casaDown bool
	leaking  bool
	vpsIP    string
}

func (r *Router) startLeakWatcher(ctx context.Context) {
	// This only makes sense if the tunnel is configured on this host.
	if r.cfg.SingboxConfigPath == "" {
		return
	}
	if _, err := os.Stat(r.cfg.SingboxConfigPath); err != nil {
		return // no tunnel here — nothing to watch
	}
	s := &leakSentinela{}
	go func() {
		// the first tick after a short delay (lets the boot settle)
		t := time.NewTimer(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			// Armour: a panic in the tick (network, parse, notify) must NEVER
			// take the control plane down. A goroutine that panics kills the whole
			// process — this watchman is surveillance, it cannot become the cause
			// of the outage. Recover, log, and carry on next cycle.
			func() {
				defer func() {
					if rec := recover(); rec != nil {
						log.Printf("leak-watcher: recovered from a panic in the tick (carrying on): %v", rec)
					}
				}()
				s.tick(r)
			}()
			t.Reset(leakInterval)
		}
	}()
	log.Printf("leak-watcher: watching the home-egress every %s", leakInterval)
}

// tick proves the house's egress IP and compares it with the VPS's.
func (s *leakSentinela) tick(r *Router) {
	// With no notification channel there is nothing for this watchman to do —
	// and calling r.notify.Dispatch with r.notify nil would be a nil deref that
	// takes the process down (the same guard as hypervisor_watcher.go). Exit.
	if r.notify == nil {
		return
	}
	vpsIP := s.ensureVPSIP()
	casaIP, err := s.casaEgressIP(r)

	s.mu.Lock()
	defer s.mu.Unlock()

	if err != nil || casaIP == "" {
		s.fails++
		if s.fails >= leakFailBefore && !s.casaDown {
			s.casaDown = true
			r.notify.Dispatch(notify.Event{
				Type: TipoTunnelDown, Severity: notify.SeverityWarning, Source: "leak-watcher",
				Title: "Tunnel home exit unreachable",
				Body:  fmt.Sprintf("The residential exit did not answer for %d cycles (last error: %v). Traffic marked for home may have no route.", s.fails, err),
				TS:    time.Now().Unix(), DedupKey: leakDedup,
			})
		}
		return
	}
	// an answer arrived → reset the down counter
	if s.casaDown {
		s.casaDown = false
		r.notify.Dispatch(notify.Event{
			Type: TipoTunnelOK, Severity: notify.SeverityInfo, Source: "leak-watcher",
			Title: "Tunnel home exit is back", Body: "Home exit IP: " + casaIP,
			TS: time.Now().Unix(), DedupKey: leakDedup,
		})
	}
	s.fails = 0

	// LEAK: the house leaving via the same IP as the VPS.
	leakNow := vpsIP != "" && casaIP == vpsIP
	if leakNow && !s.leaking {
		s.leaking = true
		r.notify.Dispatch(notify.Event{
			Type: TipoTunnelLeak, Severity: notify.SeverityCritical, Source: "leak-watcher",
			Title: "LEAK: home exit is leaving through the VPS",
			Body:  fmt.Sprintf("The home exit IP (%s) is the same as the VPS one. Traffic that should leave through the house is going out through the VPS.", casaIP),
			TS:    time.Now().Unix(), DedupKey: leakDedup,
		})
	} else if !leakNow && s.leaking {
		s.leaking = false
		r.notify.Dispatch(notify.Event{
			Type: TipoTunnelOK, Severity: notify.SeverityInfo, Source: "leak-watcher",
			Title: "Home exit back to normal", Body: "Home exit IP: " + casaIP,
			TS: time.Now().Unix(), DedupKey: leakDedup,
		})
	}
}

// ensureVPSIP discovers (once) the VPS's public IP through a direct egress.
func (s *leakSentinela) ensureVPSIP() string {
	s.mu.Lock()
	ip := s.vpsIP
	s.mu.Unlock()
	if ip != "" {
		return ip
	}
	ip = fetchIP(http.DefaultTransport)
	if ip != "" {
		s.mu.Lock()
		s.vpsIP = ip
		s.mu.Unlock()
	}
	return ip
}

// casaEgressIP dials the house's SOCKS (read from the config) and fetches the egress IP.
func (s *leakSentinela) casaEgressIP(r *Router) (string, error) {
	host, port, user, pass, err := readCasaSOCKS(r.cfg.SingboxConfigPath)
	if err != nil {
		return "", err
	}
	var auth *proxy.Auth
	if user != "" {
		auth = &proxy.Auth{User: user, Password: pass}
	}
	dialer, err := proxy.SOCKS5("tcp", net.JoinHostPort(host, port), auth, &net.Dialer{Timeout: 8 * time.Second})
	if err != nil {
		return "", err
	}
	tr := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialer.Dial(network, addr)
		},
	}
	defer tr.CloseIdleConnections()
	ip := fetchIP(tr)
	if ip == "" {
		return "", fmt.Errorf("no IP response through the casa exit")
	}
	return ip, nil
}

// fetchIP gets the egress IP through a transport (direct or via a proxy).
func fetchIP(tr http.RoundTripper) string {
	cli := &http.Client{Timeout: 10 * time.Second, Transport: tr}
	resp, err := cli.Get("http://api.ipify.org")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return ""
	}
	ip := string(b)
	if net.ParseIP(ip) == nil {
		return ""
	}
	return ip
}

// readCasaSOCKS extracts server/port/user/pass from the "casa" outbound in the config.
func readCasaSOCKS(configPath string) (host, port, user, pass string, err error) {
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return "", "", "", "", err
	}
	var doc struct {
		Outbounds []struct {
			Type     string `json:"type"`
			Tag      string `json:"tag"`
			Server   string `json:"server"`
			Port     int    `json:"server_port"`
			Username string `json:"username"`
			Password string `json:"password"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", "", "", "", err
	}
	for _, o := range doc.Outbounds {
		if o.Tag == "casa" && o.Type == "socks" {
			return o.Server, fmt.Sprintf("%d", o.Port), o.Username, o.Password, nil
		}
	}
	return "", "", "", "", fmt.Errorf("casa (socks) outbound not found in the config")
}
