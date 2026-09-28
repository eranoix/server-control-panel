package api

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"server-control-panel/internal/metrics"
	"server-control-panel/internal/pty"
	"server-control-panel/internal/queue"
	"server-control-panel/internal/scope"
	"server-control-panel/internal/system"
)

const (
	catSystem = "System"
	catJobs   = "Jobs"
	catNotify = "Notifications"
	catClaude = "Claude"
	catDocker = "Docker"
	catWA     = "WhatsApp"
	catAuth   = "Authentication"
)

func (r *Router) buildMetricRegistry() {
	reg := metrics.NewRegistry()
	reg.Register(newSystemCollector())
	reg.Register(r.jobsCollector())
	if r.notify != nil {
		reg.Register(r.notifyCollector())
	}
	reg.Register(authCollector())
	if r.whatsappMgr != nil {
		reg.Register(r.whatsappCollector())
	}
	if r.claudeAccts != nil {
		reg.Register(r.claudeCollector())
	}
	if r.docker != nil {
		reg.Register(r.dockerCollector())
	}
	reg.Register(sessionCollector())
	r.metricReg = reg
	r.metricHist = metrics.NewMetricHistory(720)
}

type systemCollector struct {
	mu          sync.Mutex
	lastStats   *system.Stats
	lastNetSent uint64
	lastNetRecv uint64
	lastNetT    int64
}

func newSystemCollector() *systemCollector { return &systemCollector{} }

func (c *systemCollector) Interval() time.Duration { return 0 }

func (c *systemCollector) Describe() []metrics.MetricDescriptor {
	d := []metrics.MetricDescriptor{
		{Key: "sys.cpu", Label: "CPU (usage)", Unit: "%", Category: catSystem, Kind: "gauge"},
		{Key: "sys.mem_pct", Label: "Memory (usage)", Unit: "%", Category: catSystem, Kind: "gauge"},
		{Key: "sys.mem_used", Label: "Memory used", Unit: "bytes", Category: catSystem, Kind: "gauge"},
		{Key: "sys.swap_pct", Label: "Swap (usage)", Unit: "%", Category: catSystem, Kind: "gauge"},
		{Key: "sys.load1", Label: "Load (1 min)", Unit: "load", Category: catSystem, Kind: "gauge"},
		{Key: "sys.load5", Label: "Load (5 min)", Unit: "load", Category: catSystem, Kind: "gauge"},
		{Key: "sys.load15", Label: "Load (15 min)", Unit: "load", Category: catSystem, Kind: "gauge"},
		{Key: "sys.disk.max", Label: "Disk — peak usage", Unit: "%", Category: catSystem, Kind: "gauge"},
		{Key: "sys.net.sent_rate", Label: "Network — upload", Unit: "bytes/s", Category: catSystem, Kind: "gauge"},
		{Key: "sys.net.recv_rate", Label: "Network — download", Unit: "bytes/s", Category: catSystem, Kind: "gauge"},
		{Key: "sys.uptime", Label: "Uptime", Unit: "s", Category: catSystem, Kind: "gauge"},
		{Key: "sys.goroutines", Label: "Goroutines (Go)", Unit: "count", Category: catSystem, Kind: "gauge"},
	}
	c.mu.Lock()
	last := c.lastStats
	c.mu.Unlock()
	if last != nil {
		for _, dk := range last.Disks {
			d = append(d, metrics.MetricDescriptor{
				Key:   "sys.disk." + mountKey(dk.Mount) + ".used_pct",
				Label: "Disk " + dk.Mount, Unit: "%", Category: catSystem, Kind: "gauge",
			})
		}
	}
	return d
}

func (c *systemCollector) Collect(ctx context.Context) map[string]float64 {
	s, err := system.Collect(ctx)
	if err != nil || s == nil {
		return nil
	}
	m := map[string]float64{
		"sys.cpu":        s.CPU.Overall,
		"sys.mem_pct":    s.Memory.UsedPercent,
		"sys.mem_used":   float64(s.Memory.Used),
		"sys.swap_pct":   s.Swap.UsedPercent,
		"sys.load1":      s.Load.Load1,
		"sys.load5":      s.Load.Load5,
		"sys.load15":     s.Load.Load15,
		"sys.uptime":     float64(s.Host.Uptime),
		"sys.goroutines": float64(s.GoRuntime.Goroutines),
	}
	var diskMax float64
	for _, dk := range s.Disks {
		if dk.UsedPercent > diskMax {
			diskMax = dk.UsedPercent
		}
		m["sys.disk."+mountKey(dk.Mount)+".used_pct"] = dk.UsedPercent
	}
	m["sys.disk.max"] = diskMax

	var sent, recv uint64
	for _, n := range s.Net {
		sent += n.BytesSent
		recv += n.BytesRecv
	}
	now := time.Now().Unix()
	c.mu.Lock()
	if c.lastNetT > 0 && now > c.lastNetT {
		dt := float64(now - c.lastNetT)
		if sent >= c.lastNetSent {
			m["sys.net.sent_rate"] = float64(sent-c.lastNetSent) / dt
		}
		if recv >= c.lastNetRecv {
			m["sys.net.recv_rate"] = float64(recv-c.lastNetRecv) / dt
		}
	}
	c.lastNetSent, c.lastNetRecv, c.lastNetT = sent, recv, now
	c.lastStats = s
	c.mu.Unlock()
	return m
}

func mountKey(mount string) string {
	if mount == "/" || mount == "" {
		return "root"
	}
	return strings.Trim(strings.ReplaceAll(mount, "/", "_"), "_")
}

func (r *Router) jobsCollector() metrics.Collector {
	desc := []metrics.MetricDescriptor{
		{Key: "jobs.queue.depth", Label: "Queue — tasks waiting", Unit: "count", Category: catJobs, Kind: "gauge"},
		{Key: "jobs.running", Label: "Tasks running", Unit: "count", Category: catJobs, Kind: "gauge"},
		{Key: "jobs.running.detached", Label: "Tasks running (detached)", Unit: "count", Category: catJobs, Kind: "gauge"},
		{Key: "jobs.failed.total", Label: "Task failures (total)", Unit: "count", Category: catJobs, Kind: "counter"},
		{Key: "jobs.done.total", Label: "Tasks completed (total)", Unit: "count", Category: catJobs, Kind: "counter"},
		{Key: "jobs.interrupted.total", Label: "Tasks interrupted", Unit: "count", Category: catJobs, Kind: "counter"},
		{Key: "jobs.duration.avg", Label: "Average task duration", Unit: "s", Category: catJobs, Kind: "gauge"},
		{Key: "scheduler.total", Label: "Schedules (total)", Unit: "count", Category: catJobs, Kind: "gauge"},
		{Key: "scheduler.enabled", Label: "Schedules active", Unit: "count", Category: catJobs, Kind: "gauge"},
		{Key: "scheduler.last_failed", Label: "Schedules whose last run failed", Unit: "count", Category: catJobs, Kind: "gauge"},
		{Key: "scheduler.past_due", Label: "Schedules overdue", Unit: "count", Category: catJobs, Kind: "gauge"},
	}
	return metrics.NewFuncCollector(0, desc, func(context.Context) map[string]float64 {
		m := map[string]float64{}
		if r.queue != nil {
			running, queued := r.queue.Counts()
			m["jobs.running"] = float64(running)
			m["jobs.queue.depth"] = float64(queued)
			var failed, done, interrupted, detached int
			var durSum, durN int64
			for _, j := range r.queue.List("", "", 0) {
				switch j.Status {
				case queue.StatusFailed:
					failed++
				case queue.StatusDone:
					done++
					if j.Finished > j.Started && j.Started > 0 {
						durSum += j.Finished - j.Started
						durN++
					}
				case queue.StatusInterrupted:
					interrupted++
				}
				if j.Status == queue.StatusRunning && j.Scope != "" {
					detached++
				}
			}
			m["jobs.failed.total"] = float64(failed)
			m["jobs.done.total"] = float64(done)
			m["jobs.interrupted.total"] = float64(interrupted)
			m["jobs.running.detached"] = float64(detached)
			if durN > 0 {
				m["jobs.duration.avg"] = float64(durSum) / float64(durN)
			}
		}
		if r.scheduler != nil {
			now := time.Now().Unix()
			var enabled, lastFailed, pastDue int
			sched := r.scheduler.List("")
			for _, j := range sched {
				if j.Enabled {
					enabled++
				}
				if j.LastStatus == "failed" {
					lastFailed++
				}
				if j.Enabled && j.NextFire > 0 && j.NextFire <= now {
					pastDue++
				}
			}
			m["scheduler.total"] = float64(len(sched))
			m["scheduler.enabled"] = float64(enabled)
			m["scheduler.last_failed"] = float64(lastFailed)
			m["scheduler.past_due"] = float64(pastDue)
		}
		return m
	})
}

func (r *Router) notifyCollector() metrics.Collector {
	desc := []metrics.MetricDescriptor{
		{Key: "notify.dropped", Label: "Notifications dropped (overload)", Unit: "count", Category: catNotify, Kind: "counter"},
		{Key: "notify.inbox", Label: "Inbox (unread in the panel)", Unit: "count", Category: catNotify, Kind: "gauge"},
		{Key: "notify.channels", Label: "Channels configured", Unit: "count", Category: catNotify, Kind: "gauge"},
		{Key: "notify.rules", Label: "Notification rules", Unit: "count", Category: catNotify, Kind: "gauge"},
	}
	return metrics.NewFuncCollector(0, desc, func(context.Context) map[string]float64 {
		if r.notify == nil {
			return nil
		}
		return map[string]float64{
			"notify.dropped":  float64(r.notify.Dropped()),
			"notify.inbox":    float64(len(r.notify.Inbox(1 << 20))),
			"notify.channels": float64(len(r.notify.ChannelDefs())),
			"notify.rules":    float64(len(r.notify.Rules())),
		}
	})
}

func authCollector() metrics.Collector {
	desc := []metrics.MetricDescriptor{
		{Key: "auth.login_attempts", Label: "Login attempts", Unit: "count", Category: catAuth, Kind: "counter"},
		{Key: "auth.login_failures", Label: "Login failures", Unit: "count", Category: catAuth, Kind: "counter"},
	}
	return metrics.NewFuncCollector(0, desc, func(context.Context) map[string]float64 {
		return map[string]float64{
			"auth.login_attempts": float64(atomic.LoadInt64(&metricLoginAttempts)),
			"auth.login_failures": float64(atomic.LoadInt64(&metricLoginFailures)),
		}
	})
}

func (r *Router) whatsappCollector() metrics.Collector {
	desc := []metrics.MetricDescriptor{
		{Key: "wa.services.running", Label: "WhatsApp sessions up", Unit: "count", Category: catWA, Kind: "gauge"},
	}
	return metrics.NewFuncCollector(0, desc, func(context.Context) map[string]float64 {
		if r.whatsappMgr == nil {
			return nil
		}
		var running int
		for _, u := range r.cfg.AllUsers() {
			if r.whatsappMgr.LookupRunning(scope.User(u.Username)) != nil {
				running++
			}
		}
		return map[string]float64{"wa.services.running": float64(running)}
	})
}

var claudeQuotaWindows = []struct{ key, short, label string }{
	{"five_hour", "5h", "5h quota"},
	{"seven_day", "7d", "Weekly quota"},
	{"seven_day_opus", "7d_opus", "Weekly Opus quota"},
	{"seven_day_sonnet", "7d_sonnet", "Weekly Sonnet quota"},
	{"seven_day_oauth_apps", "7d_apps", "Weekly apps quota"},
}

func claudeQuotaShort(key string) string {
	for _, w := range claudeQuotaWindows {
		if w.key == key {
			return w.short
		}
	}
	return ""
}

func (r *Router) claudeCollector() metrics.Collector {
	desc := []metrics.MetricDescriptor{
		{Key: "claude.cost.today", Label: "Cost (today)", Unit: "USD", Category: catClaude, Kind: "gauge"},
		{Key: "claude.cost.7d", Label: "Cost (7 days)", Unit: "USD", Category: catClaude, Kind: "gauge"},
		{Key: "claude.cost.total", Label: "Cost (total)", Unit: "USD", Category: catClaude, Kind: "counter"},
		{Key: "claude.tokens.today", Label: "Tokens (today)", Unit: "tokens", Category: catClaude, Kind: "gauge"},
		{Key: "claude.tokens.7d", Label: "Tokens (7 days)", Unit: "tokens", Category: catClaude, Kind: "gauge"},
		{Key: "claude.messages.today", Label: "Messages (today)", Unit: "count", Category: catClaude, Kind: "gauge"},
	}
	for _, w := range claudeQuotaWindows {
		desc = append(desc, metrics.MetricDescriptor{Key: "claude.quota." + w.short, Label: w.label + " (max)", Unit: "%", Category: catClaude, Kind: "gauge"})
	}
	for _, acct := range r.claudeAccts.Accounts() {
		id := acct.ID
		cat := catClaude + " · " + id
		desc = append(desc,
			metrics.MetricDescriptor{Key: "claude.cost.today." + id, Label: "Cost (today)", Unit: "USD", Category: cat, Kind: "gauge"},
			metrics.MetricDescriptor{Key: "claude.tokens.today." + id, Label: "Tokens (today)", Unit: "tokens", Category: cat, Kind: "gauge"},
		)
		for _, w := range claudeQuotaWindows {
			desc = append(desc, metrics.MetricDescriptor{Key: "claude.quota." + w.short + "." + id, Label: w.label, Unit: "%", Category: cat, Kind: "gauge"})
		}
	}
	return metrics.NewFuncCollector(60*time.Second, desc, func(context.Context) map[string]float64 {
		if r.claudeAccts == nil {
			return nil
		}
		m := map[string]float64{}
		var costToday, cost7d, costTotal, tokToday, tok7d, msgToday float64
		aggQuota := map[string]float64{}
		for _, acct := range r.claudeAccts.Accounts() {
			u := r.claudeAccts.Usage(acct)
			costToday += u.Today.CostUSD
			cost7d += u.Last7d.CostUSD
			costTotal += u.Total.CostUSD
			tokToday += float64(u.Today.TotalTokens())
			tok7d += float64(u.Last7d.TotalTokens())
			msgToday += float64(u.Today.Messages)
			m["claude.cost.today."+acct.ID] = u.Today.CostUSD
			m["claude.tokens.today."+acct.ID] = float64(u.Today.TotalTokens())

			rl := r.claudeAccts.RateLimits(acct)
			for _, w := range rl.Windows {
				short := claudeQuotaShort(w.Key)
				if short == "" {
					continue
				}
				m["claude.quota."+short+"."+acct.ID] = w.UtilizationPct
				if w.UtilizationPct > aggQuota[short] {
					aggQuota[short] = w.UtilizationPct
				}
			}
		}
		m["claude.cost.today"] = costToday
		m["claude.cost.7d"] = cost7d
		m["claude.cost.total"] = costTotal
		m["claude.tokens.today"] = tokToday
		m["claude.tokens.7d"] = tok7d
		m["claude.messages.today"] = msgToday
		for short, v := range aggQuota {
			m["claude.quota."+short] = v
		}
		return m
	})
}

func (r *Router) dockerCollector() metrics.Collector {
	desc := []metrics.MetricDescriptor{
		{Key: "docker.running", Label: "Containers running", Unit: "count", Category: catDocker, Kind: "gauge"},
		{Key: "docker.stopped", Label: "Stopped containers", Unit: "count", Category: catDocker, Kind: "gauge"},
		{Key: "docker.total", Label: "Containers (total)", Unit: "count", Category: catDocker, Kind: "gauge"},
	}
	return metrics.NewFuncCollector(30*time.Second, desc, func(ctx context.Context) map[string]float64 {
		if r.docker == nil {
			return nil
		}
		list, err := r.docker.ListContainers(ctx)
		if err != nil {
			return nil
		}
		var running int
		for _, ct := range list {
			if ct.State == "running" {
				running++
			}
		}
		return map[string]float64{
			"docker.running": float64(running),
			"docker.total":   float64(len(list)),
			"docker.stopped": float64(len(list) - running),
		}
	})
}

func sessionCollector() metrics.Collector {
	desc := []metrics.MetricDescriptor{
		{Key: "sessions.active", Label: "Sessions (dtach)", Unit: "count", Category: catSystem, Kind: "gauge"},
	}
	return metrics.NewFuncCollector(30*time.Second, desc, func(context.Context) map[string]float64 {
		sessions, err := pty.SessionListAll()
		if err != nil {
			return nil
		}
		return map[string]float64{"sessions.active": float64(len(sessions))}
	})
}
