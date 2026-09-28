package nodeagent

import (
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"net/http"
	"os"
	"strings"
)

const maxHeaderSize = 4096

type Secret struct {
	hash    [sha256.Size]byte
	present bool
}

func SecretFromFile(path string) (Secret, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Secret{}, nil
		}
		return Secret{}, fmt.Errorf("reading the bearer from %s: %w", path, err)
	}
	return SecretFromText(strings.TrimSpace(string(b))), nil
}

func SecretFromText(token string) Secret {
	if token == "" {
		return Secret{}
	}
	return Secret{hash: sha256.Sum256([]byte(token)), present: true}
}

func (s Secret) Present() bool { return s.present }

func (s Secret) matches(presented string) bool {
	if !s.present {
		return false
	}
	theirs := sha256.Sum256([]byte(presented))
	return subtle.ConstantTimeCompare(s.hash[:], theirs[:]) == 1
}

func RequireBearer(s Secret, nextID http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.present {
			unauthorized(w, "agent has no provisioned secret")
			return
		}
		cab := r.Header.Get("Authorization")
		if len(cab) > maxHeaderSize {
			unauthorized(w, "malformed credential")
			return
		}
		const prefix = "Bearer "
		if !strings.HasPrefix(cab, prefix) {
			unauthorized(w, "credential missing")
			return
		}
		if !s.matches(strings.TrimSpace(cab[len(prefix):])) {
			unauthorized(w, "invalid credential")
			return
		}
		nextID.ServeHTTP(w, r)
	})
}

func unauthorized(w http.ResponseWriter, _ string) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="node-agent"`)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
}
