package mobilebff

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"server-control-panel/internal/auth"
)

const idempotencyHeader = "Idempotency-Key"

func idempotencyKey(user, header string) string {
	c := strings.TrimSpace(header)
	if c == "" || user == "" {
		return ""
	}
	return user + ":" + c
}

func rememberResult[T any](idem *Idempotency, key string, fn func() (*T, error)) (*T, error) {
	if idem == nil || key == "" {
		return fn()
	}
	if body, _, ok := idem.Recall(key); ok {
		var stored T
		if err := json.Unmarshal([]byte(body), &stored); err == nil {
			return &stored, nil
		}
	}
	out, err := fn()
	if err != nil || out == nil {
		return out, err
	}
	if body, err := json.Marshal(out); err == nil {
		idem.Remember(key, string(body), http.StatusOK)
	}
	return out, nil
}

func keyFromContext(ctx context.Context, header string) string {
	return idempotencyKey(auth.UserFromContext(ctx), header)
}
