package fcmpush

import (
	"context"
	"encoding/json"
	"fmt"
)

type DataOptions struct {
	CollapseKey string
	TTLSeconds  int
}

func (s *Sender) SendDataToUser(ctx context.Context, user string, data map[string]string, opts DataOptions, allowDevice func(deviceID string) bool) int {
	if s == nil || s.store == nil {
		return 0
	}
	return s.sendData(ctx, s.store.TokensForUser(user), data, opts, allowDevice)
}

func (s *Sender) SendDataToAll(ctx context.Context, data map[string]string, opts DataOptions, allowDevice func(deviceID string) bool) int {
	if s == nil || s.store == nil {
		return 0
	}
	return s.sendData(ctx, s.store.AllTokens(), data, opts, allowDevice)
}

func (s *Sender) sendData(ctx context.Context, tokens []DeviceToken, data map[string]string, opts DataOptions, allowDevice func(string) bool) int {
	return s.deliver(ctx, tokens, allowDevice, func(token string) ([]byte, error) {
		return buildDataMessageBody(token, data, opts)
	})
}

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
