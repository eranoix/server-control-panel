// Package webpush provides a generic, channel-agnostic Web Push sender: a
// persisted VAPID keypair plus a per-user subscription store.
//
// This existed only inside internal/videocall (as the private "pushStore"
// that powers the incoming-call ring) until it was extracted here so
// internal/notify could reuse it for a generic "push" channel.
// internal/webassets/serviceworker.go already handles an "alert-fired"
// push payload that no Go code has ever published, precisely because there
// was no shared way to reach a subscribed browser from outside videocall.
//
// Storage (caller decides the root directory — this package does not
// hardcode a path):
//
//	<root>/vapid.json       — public/private VAPID keypair (single)
//	<root>/push-subs.json   — array of subscriptions, keyed by endpoint
//
// Lifecycle:
//   - VAPID keys generated on first Open() if vapid.json is missing.
//   - Subs added via Add(), removed via Remove() or automatically on a
//     410 Gone / 404 Not Found response from the push service.
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

// vapidKeys is the persisted ECDSA-P256 keypair we identify ourselves with
// to push services. Same pair for the lifetime of the install; rotating
// would invalidate every existing subscription.
type vapidKeys struct {
	Public  string `json:"public"`
	Private string `json:"private"`
	Subject string `json:"subject"` // mailto:... for compliance
}

// PushSubscription matches the W3C PushSubscription JSON the browser
// returns from PushManager.subscribe(). Stored verbatim plus a User stamp.
type PushSubscription struct {
	User     string               `json:"user"`
	Endpoint string               `json:"endpoint"`
	Keys     PushSubscriptionKeys `json:"keys"`
	// DeviceID ties the subscription to the DEVICE. Without that link,
	// silencing "this computer" would mute only the in-tab ring, and the operating
	// system push would keep popping on the same device.
	// Empty on subscriptions predating the field → never silenced.
	DeviceID  string `json:"device_id,omitempty"`
	UserAgent string `json:"user_agent,omitempty"`
	CreatedAt int64  `json:"created_at"`
}

type PushSubscriptionKeys struct {
	P256dh string `json:"p256dh"`
	Auth   string `json:"auth"`
}

// SendOptions parameterizes one send — the three fields that already varied per
// call in the old SendIncomingCall (TTL, Topic and Urgency). Subscriber and
// the VAPID keys stay internal to Store; callers never see them.
type SendOptions struct {
	TTL     int
	Topic   string
	Urgency webpush.Urgency
}

// Store holds the VAPID pair + the list of subscriptions, with a
// background flusher that batches writes off the hot path.
type Store struct {
	mu        sync.RWMutex
	keys      vapidKeys
	subs      map[string]*PushSubscription // endpoint -> sub
	rootPath  string
	keysPath  string
	subsPath  string
	dirtySubs bool
	stop      chan struct{}
}

// Open creates root (if missing), loads/generates the VAPID keypair and
// loads any persisted subscriptions, then starts a 5s background flusher.
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

// Close signals the flusher to stop and does a final flush.
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
	// Generate a fresh pair.
	priv, pub, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		return fmt.Errorf("vapid keygen: %w", err)
	}
	p.keys = vapidKeys{Public: pub, Private: priv, Subject: "mailto:videocall@vps-manager.local"}
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
		return nil // tolerant
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

// PublicKey returns the VAPID public key in base64-url-encoded raw form
// — the format the browser PushManager.subscribe() expects in the
// applicationServerKey field.
func (p *Store) PublicKey() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.keys.Public
}

// Add inserts or updates a subscription. Idempotent — re-subscribing the
// same endpoint just refreshes the UA / user fields. New subs are written
// immediately so a server restart right after subscribe doesn't lose them.
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
	_ = p.saveSubs() // synchronous on subscribe — important
}

// Remove deletes a subscription by endpoint. Used when the user explicitly
// unsubscribes OR when a push attempt returns 410 Gone.
func (p *Store) Remove(endpoint string) {
	p.mu.Lock()
	if _, ok := p.subs[endpoint]; ok {
		delete(p.subs, endpoint)
		p.dirtySubs = true
	}
	p.mu.Unlock()
}

// ForUser returns a snapshot of the user's active subs.
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

// all returns a snapshot of every stored subscription, regardless of user.
func (p *Store) all() []*PushSubscription {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]*PushSubscription, 0, len(p.subs))
	for _, s := range p.subs {
		out = append(out, s)
	}
	return out
}

// send delivers payload to every sub in subs, filtered by allowDevice.
// Returns the number of pushes successfully delivered (HTTP 2xx).
// Subscriptions returning 410/404 are auto-removed.
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
		Topic:           opts.Topic, // collapses duplicates at the push service
		Urgency:         opts.Urgency,
	}
	delivered := 0
	for _, s := range subs {
		if allowDevice != nil && !allowDevice(s.DeviceID) {
			continue // device silenced by its owner
		}
		// Translate our PushSubscription to webpush-go's struct shape.
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
		// 2xx = delivered; 410/404 = subscription dead → remove.
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			delivered++
		} else if resp.StatusCode == 410 || resp.StatusCode == 404 {
			p.Remove(s.Endpoint)
		}
		_ = resp.Body.Close()
	}
	return delivered
}

// SendToUser fires payload to every active subscription for user. Returns
// the number of pushes successfully delivered (HTTP 2xx). `allowDevice`
// filters by device policy; nil = send to all.
func (p *Store) SendToUser(ctx context.Context, user string, payload []byte, opts SendOptions, allowDevice func(deviceID string) bool) int {
	return p.send(ctx, p.ForUser(user), payload, opts, allowDevice)
}

// SendToAll fires payload to every stored subscription, regardless of
// user. Returns the number of pushes successfully delivered (HTTP 2xx).
// filters by device policy; nil = send to all.
func (p *Store) SendToAll(ctx context.Context, payload []byte, opts SendOptions, allowDevice func(deviceID string) bool) int {
	return p.send(ctx, p.all(), payload, opts, allowDevice)
}

// ---- utilities ---------------------------------------------------------

// atomicWriteJSON is a deliberate copy of the version in internal/videocall —
// that package keeps its own (used by devices.go,
// livecall.go and recordings.go), so we duplicate it here instead of coupling the
// two packages over a 25-line function.
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
	// Fsync on the parent directory to make sure the link is persisted (without
	// it, on a crash between Rename and shutdown, the file can vanish
	// even with Sync).
	if df, err := os.Open(filepath.Dir(path)); err == nil {
		_ = df.Sync()
		_ = df.Close()
	}
	return nil
}
