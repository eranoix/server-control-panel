package webpush

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
)

type vapidKeys struct {
	Public  string `json:"public"`
	Private string `json:"private"`
	Subject string `json:"subject"`
}

type PushSubscription struct {
	User      string               `json:"user"`
	Endpoint  string               `json:"endpoint"`
	Keys      PushSubscriptionKeys `json:"keys"`
	DeviceID  string               `json:"device_id,omitempty"`
	UserAgent string               `json:"user_agent,omitempty"`
	CreatedAt int64                `json:"created_at"`
}

type PushSubscriptionKeys struct {
	P256dh string `json:"p256dh"`
	Auth   string `json:"auth"`
}

type SendOptions struct {
	TTL     int
	Topic   string
	Urgency webpush.Urgency
}

type Store struct {
	mu        sync.RWMutex
	keys      vapidKeys
	subs      map[string]*PushSubscription
	rootPath  string
	keysPath  string
	subsPath  string
	dirtySubs bool
	stop      chan struct{}
}

func Open(root string) (*Store, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	p := &Store{
		subs:     make(map[string]*PushSubscription),
		rootPath: root,
		keysPath: filepath.Join(root, "vapid.json"),
		subsPath: filepath.Join(root, "push-subs.json"),
		stop:     make(chan struct{}),
	}
	if err := p.loadKeys(); err != nil {
		return nil, err
	}
	_ = p.loadSubs()
	go p.flusher()
	return p, nil
}

func (p *Store) Close() error {
	if p == nil {
		return nil
	}
	select {
	case <-p.stop:
	default:
		close(p.stop)
	}
	return p.saveSubs()
}

func (p *Store) loadKeys() error {
	b, err := os.ReadFile(p.keysPath)
	if err == nil {
		if json.Unmarshal(b, &p.keys) == nil && p.keys.Public != "" && p.keys.Private != "" {
			return nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	priv, pub, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		return fmt.Errorf("vapid keygen: %w", err)
	}
	p.keys = vapidKeys{Public: pub, Private: priv, Subject: "mailto:videocall@server-control-panel.local"}
	return atomicWriteJSON(p.keysPath, p.keys, 0o600)
}

func (p *Store) loadSubs() error {
	b, err := os.ReadFile(p.subsPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var arr []*PushSubscription
	if err := json.Unmarshal(b, &arr); err != nil {
		return nil
	}
	for _, s := range arr {
		if s.Endpoint == "" {
			continue
		}
		p.subs[s.Endpoint] = s
	}
	return nil
}

func (p *Store) flusher() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[webpush] flusher panic recovered: %v", r)
		}
	}()
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-t.C:
			p.mu.Lock()
			need := p.dirtySubs
			p.dirtySubs = false
			p.mu.Unlock()
			if need {
				_ = p.saveSubs()
			}
		}
	}
}

func (p *Store) saveSubs() error {
	p.mu.RLock()
	arr := make([]*PushSubscription, 0, len(p.subs))
	for _, s := range p.subs {
		arr = append(arr, s)
	}
	p.mu.RUnlock()
	sort.Slice(arr, func(i, j int) bool { return arr[i].CreatedAt < arr[j].CreatedAt })
	return atomicWriteJSON(p.subsPath, arr, 0o600)
}

func (p *Store) PublicKey() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.keys.Public
}

func (p *Store) Add(sub *PushSubscription) {
	if sub == nil || sub.Endpoint == "" || sub.User == "" {
		return
	}
	if sub.CreatedAt == 0 {
		sub.CreatedAt = time.Now().Unix()
	}
	p.mu.Lock()
	p.subs[sub.Endpoint] = sub
	p.dirtySubs = true
	p.mu.Unlock()
	_ = p.saveSubs()
}

func (p *Store) Remove(endpoint string) {
	p.mu.Lock()
	if _, ok := p.subs[endpoint]; ok {
		delete(p.subs, endpoint)
		p.dirtySubs = true
	}
	p.mu.Unlock()
}

func (p *Store) ForUser(user string) []*PushSubscription {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]*PushSubscription, 0)
	for _, s := range p.subs {
		if s.User == user {
			out = append(out, s)
		}
	}
	return out
}

func (p *Store) all() []*PushSubscription {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]*PushSubscription, 0, len(p.subs))
	for _, s := range p.subs {
		out = append(out, s)
	}
	return out
}

func (p *Store) send(ctx context.Context, subs []*PushSubscription, payload []byte, opts SendOptions, allowDevice func(deviceID string) bool) int {
	if len(subs) == 0 {
		return 0
	}
	p.mu.RLock()
	pub, priv, subj := p.keys.Public, p.keys.Private, p.keys.Subject
	p.mu.RUnlock()
	wpOpts := &webpush.Options{
		Subscriber:      subj,
		VAPIDPublicKey:  pub,
		VAPIDPrivateKey: priv,
		TTL:             opts.TTL,
		Topic:           opts.Topic,
		Urgency:         opts.Urgency,
	}
	delivered := 0
	for _, s := range subs {
		if allowDevice != nil && !allowDevice(s.DeviceID) {
			continue
		}
		wpSub := &webpush.Subscription{
			Endpoint: s.Endpoint,
			Keys: webpush.Keys{
				P256dh: s.Keys.P256dh,
				Auth:   s.Keys.Auth,
			},
		}
		sendCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		resp, err := webpush.SendNotificationWithContext(sendCtx, payload, wpSub, wpOpts)
		cancel()
		if err != nil {
			continue
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			delivered++
		} else if resp.StatusCode == 410 || resp.StatusCode == 404 {
			p.Remove(s.Endpoint)
		}
		_ = resp.Body.Close()
	}
	return delivered
}

func (p *Store) SendToUser(ctx context.Context, user string, payload []byte, opts SendOptions, allowDevice func(deviceID string) bool) int {
	return p.send(ctx, p.ForUser(user), payload, opts, allowDevice)
}

func (p *Store) SendToAll(ctx context.Context, payload []byte, opts SendOptions, allowDevice func(deviceID string) bool) int {
	return p.send(ctx, p.all(), payload, opts, allowDevice)
}

func atomicWriteJSON(path string, v any, mode os.FileMode) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".new"
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	_ = f.Close()
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if df, err := os.Open(filepath.Dir(path)); err == nil {
		_ = df.Sync()
		_ = df.Close()
	}
	return nil
}
