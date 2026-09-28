package api

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"server-control-panel/internal/notify"
	"server-control-panel/internal/singbox"
)

const (
	dsWatchInterval = 3 * time.Minute
	dsWatchFailK    = 2
)

type dsWatch struct {
	mu    sync.Mutex
	fails map[string]int
}

func (r *Router) startDatasaverWatcher(ctx context.Context) {
	if r.cfg.SingboxConfigPath == "" {
		return
	}
	if _, err := os.Stat(r.cfg.SingboxConfigPath); err != nil {
		return
	}
	st := &dsWatch{fails: map[string]int{}}
	go func() {
		t := time.NewTimer(45 * time.Second)
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
						log.Printf("datasaver-watcher: recovered from a panic in the tick (carrying on): %v", rec)
					}
				}()
				st.tick(r)
			}()
			t.Reset(dsWatchInterval)
		}
	}()
	log.Printf("datasaver-watcher: watching the data-saver proxies every %s", dsWatchInterval)
}

func (st *dsWatch) tick(r *Router) {
	mgr := r.singboxManager()
	devs, err := mgr.List()
	if err != nil {
		return
	}
	onByExit := map[string][]singbox.Device{}
	for _, d := range devs {
		if d.Datasaver {
			onByExit[d.Exit] = append(onByExit[d.Exit], d)
		}
	}
	for exit, list := range onByExit {
		_, perr := r.probeDatasaverProxy(context.Background(), exit)
		st.mu.Lock()
		if perr == nil {
			st.fails[exit] = 0
			st.mu.Unlock()
			continue
		}
		st.fails[exit]++
		n := st.fails[exit]
		st.mu.Unlock()
		if n < dsWatchFailK {
			continue
		}
		var reverted []string
		for _, d := range list {
			if e := mgr.SetDatasaver(context.Background(), d.UUID, false); e == nil {
				reverted = append(reverted, d.Name)
			} else {
				log.Printf("datasaver-watcher: failed to roll back %s: %v", d.Name, e)
			}
		}
		st.mu.Lock()
		st.fails[exit] = 0
		st.mu.Unlock()
		if len(reverted) > 0 && r.notify != nil {
			r.notify.Dispatch(notify.Event{
				Type: "datasaver.autorevert", Severity: notify.SeverityWarning, Source: "datasaver-watcher",
				Title: "Data saver auto-disabled (proxy went down)",
				Body: fmt.Sprintf("The proxy for exit %q stopped responding (%v). Switched back to DIRECT mode so the connection stays up: %s. Turn data saver back on in the panel once the proxy is back.",
					exit, perr, strings.Join(reverted, ", ")),
				TS: time.Now().Unix(), DedupKey: "datasaver:autorevert:" + exit,
			})
		}
	}
}
