package fcmpush

import (
	"context"
	"encoding/json"
	"fmt"
)

// DataOptions parametrizes a strictly data-only FCM v1 send — used for
// events that must invoke the app's own onMessageReceived in every process
// state (foreground, background, killed), which a notification-bearing
// message does not guarantee. Unlike SendOptions (SendToUser/SendToAll,
// which build the same data-only shape but with no per-message
// collapse/expiry), DataOptions models the two FCM v1 android envelope
// fields a call ring needs: CollapseKey (so a re-ring for the same call
// replaces the pending one instead of stacking) and TTLSeconds (so a ring
// nobody answers stops being deliverable once it's no longer relevant).
type DataOptions struct {
	CollapseKey string
	TTLSeconds  int
}

// SendDataToUser fires a data-only message to every FCM-registered device
// for user, always at "high" priority — matching how SendToUser/SendToAll
// already resolve tokens and auth, so a caller cannot tell the two send
// paths apart except by payload shape. Returns the number of pushes FCM
// accepted (HTTP 2xx). allowDevice filters by device policy; nil = send to
// every one of the user's devices.
func (s *Sender) SendDataToUser(ctx context.Context, user string, data map[string]string, opts DataOptions, allowDevice func(deviceID string) bool) int {
	if s == nil || s.store == nil {
		return 0
	}
	return s.sendData(ctx, s.store.TokensForUser(user), data, opts, allowDevice)
}

// SendDataToAll fires a data-only message to every registered device,
// regardless of user. Returns the number of pushes FCM accepted (HTTP 2xx).
// allowDevice filters by device policy; nil = send to everyone.
func (s *Sender) SendDataToAll(ctx context.Context, data map[string]string, opts DataOptions, allowDevice func(deviceID string) bool) int {
	if s == nil || s.store == nil {
		return 0
	}
	return s.sendData(ctx, s.store.AllTokens(), data, opts, allowDevice)
}

// sendData shares deliver's token-iteration/auth/HTTP loop with
// SendToUser/SendToAll (see sender.go), differing only in how each
// message body is built.
func (s *Sender) sendData(ctx context.Context, tokens []DeviceToken, data map[string]string, opts DataOptions, allowDevice func(string) bool) int {
	return s.deliver(ctx, tokens, allowDevice, func(token string) ([]byte, error) {
		return buildDataMessageBody(token, data, opts)
	})
}

// buildDataMessageBody shapes a strictly data-only FCM v1 request body:
//
//	{"message": {"token": "...", "data": {...}, "android": {"priority":
//	"high", "collapse_key": "...", "ttl": "<TTLSeconds>s"}}}
//
// There is deliberately NO top-level "notification" key here, ever — that
// is what lets the Android client's own onMessageReceived decide the UI
// (Telecom/CallStyle) instead of the OS auto-rendering a plain banner while
// the app is backgrounded or killed.
func buildDataMessageBody(token string, data map[string]string, opts DataOptions) ([]byte, error) {
	android := map[string]any{"priority": "high"}
	if opts.CollapseKey != "" {
		android["collapse_key"] = opts.CollapseKey
	}
	if opts.TTLSeconds > 0 {
		android["ttl"] = fmt.Sprintf("%ds", opts.TTLSeconds)
	}
	msg := map[string]any{
		"message": map[string]any{
			"token":   token,
			"data":    data,
			"android": android,
		},
	}
	return json.Marshal(msg)
}
