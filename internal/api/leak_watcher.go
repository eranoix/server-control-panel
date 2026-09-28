package api

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
	leakFailBefore = 3
	leakDedup      = "tunnel:leak"
	TypeTunnelLeak = "tunnel.leak"
	TypeTunnelDown = "tunnel.home_down"
	TypeTunnelOK   = "tunnel.recovered"
)

type leakSentinel struct {
	mu       sync.Mutex
	fails    int
	homeDown bool
	leaking  bool
	vpsIP    string
}

func (r *Router) startLeakWatcher(ctx context.Context) {
	if r.cfg.SingboxConfigPath == "" {
		return
	}
	if _, err := os.Stat(r.cfg.SingboxConfigPath); err != nil {
		return
	}
	s := &leakSentinel{}
	go func() {
		t := time.NewTimer(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
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

func (s *leakSentinel) tick(r *Router) {
	if r.notify == nil {
		return
	}
	vpsIP := s.ensureVPSIP()
	homeIP, err := s.homeEgressIP(r)

	s.mu.Lock()
	defer s.mu.Unlock()

	if err != nil || homeIP == "" {
		s.fails++
		if s.fails >= leakFailBefore && !s.homeDown {
			s.homeDown = true
			r.notify.Dispatch(notify.Event{
				Type: TypeTunnelDown, Severity: notify.SeverityWarning, Source: "leak-watcher",
				Title: "Tunnel home exit unreachable",
				Body:  fmt.Sprintf("The residential exit did not answer for %d cycles (last error: %v). Traffic marked for home may have no route.", s.fails, err),
				TS:    time.Now().Unix(), DedupKey: leakDedup,
			})
		}
		return
	}
	if s.homeDown {
		s.homeDown = false
		r.notify.Dispatch(notify.Event{
			Type: TypeTunnelOK, Severity: notify.SeverityInfo, Source: "leak-watcher",
			Title: "Tunnel home exit is back", Body: "Home exit IP: " + homeIP,
			TS: time.Now().Unix(), DedupKey: leakDedup,
		})
	}
	s.fails = 0

	leakNow := vpsIP != "" && homeIP == vpsIP
	if leakNow && !s.leaking {
		s.leaking = true
		r.notify.Dispatch(notify.Event{
			Type: TypeTunnelLeak, Severity: notify.SeverityCritical, Source: "leak-watcher",
			Title: "LEAK: home exit is leaving through the VPS",
			Body:  fmt.Sprintf("The home exit IP (%s) is the same as the VPS one. Traffic that should leave through the house is going out through the VPS.", homeIP),
			TS:    time.Now().Unix(), DedupKey: leakDedup,
		})
	} else if !leakNow && s.leaking {
		s.leaking = false
		r.notify.Dispatch(notify.Event{
			Type: TypeTunnelOK, Severity: notify.SeverityInfo, Source: "leak-watcher",
			Title: "Home exit back to normal", Body: "Home exit IP: " + homeIP,
			TS: time.Now().Unix(), DedupKey: leakDedup,
		})
	}
}

func (s *leakSentinel) ensureVPSIP() string {
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

func (s *leakSentinel) homeEgressIP(r *Router) (string, error) {
	host, port, user, pass, err := readHomeSOCKS(r.cfg.SingboxConfigPath)
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
		return "", fmt.Errorf("no IP response through the home exit")
	}
	return ip, nil
}

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

func readHomeSOCKS(configPath string) (host, port, user, pass string, err error) {
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
		if o.Tag == "home" && o.Type == "socks" {
			return o.Server, fmt.Sprintf("%d", o.Port), o.Username, o.Password, nil
		}
	}
	return "", "", "", "", fmt.Errorf("home (socks) outbound not found in the config")
}
