package telemetry

// ─── MEASUREMENT REDONE IN THIS FORK — sendBeacon × fetch(keepalive) ─────────
//
// The two forks diverged; the answer measured on the VM's panel does NOT count as
// proof here. Measured on `vps-manager` itself, against its own source tree. What
// the measurement found:
//
// (1) Is there CSRF middleware on POST /api/*?  NO.
//
//	$ grep -rniE 'csrf' /opt/panel/internal/httpmw/
//	(no output)          ← httpmw has 4 files: bandwidth, compress, nostore, security
//
//	Every match for 'csrf' across internal/ is, one by one, something else:
//	  - internal/pty/pty.go:47, internal/videocall/ws.go:34,
//	    internal/whatsapp/ws.go:26 → Origin check on the WebSocket upgrade
//	  - internal/jira/attachments.go:5,35 → X-Atlassian-Token on an OUTBOUND call
//	  - internal/api/api.go:563, handlers_auth.go:316 → comments about SameSite=Lax
//
// (2) How does this fork authenticate?  OVER TWO CHANNELS — and this is where it DIVERGES
//
//	from the VM's fork. `internal/auth/auth.go:376-390` (extractToken) accepts
//	`Authorization: Bearer` AND the `vpsm_token` cookie; `internal/httpx/cookies.go`
//	emits that cookie as HttpOnly, Secure, Path=/, SameSite=Lax, and
//	`internal/api/handlers_auth.go` sets it in 3 places (login, refresh, MFA).
//	The SPA keeps the JWT in localStorage and sends Bearer on every call
//	(00-shell.js, the api() method).
//
// BRANCH CHOSEN: `fetch(..., {keepalive:true})` with the Bearer header as the
// PRIMARY path, and `navigator.sendBeacon` as the fallback. Reason: in this fork
// the first-class authentication channel is the Bearer from localStorage — the
// cookie does exist, but relying on it alone would make telemetry die silently in
// any scenario where it isn't there (session restored by token refresh, access
// without HTTPS, cookie expired before the JWT). `keepalive` survives
// pagehide/visibilitychange just like sendBeacon, with the same 64 KiB ceiling.
//
// CONSEQUENCE HERE: the handler has to accept BOTH Content-Types —
// `application/json` (fetch) and `text/plain;charset=UTF-8` (sendBeacon with a
// string). The allowlist below already covers both, with no change in logic.
//
// A note in favour of the elevation threat: the absence of CSRF does not open this route.
// It sits inside the `protected` mux (ASVS V3) and the cookie is SameSite=Lax, which does
// not travel on cross-site POSTs — a beacon forged from another site arrives with no cookie
// and no Bearer, and takes a 401 from the login gate before this handler exists for it.
// ──────────────────────────────────────────────────────────────────────────────

import (
	"encoding/json"
	"mime"
	"net/http"
	"regexp"
	"time"
)

const (
	maxBody   = 64 << 10 // 64 KiB — same ceiling as sendBeacon (MDN)
	maxEvents = 200      // a batch above this is refused WHOLE
	schemaVer = 1
)

var reSID = regexp.MustCompile(`^[a-f0-9]{8,32}$`)

// tiposAceitos is the Content-Type allowlist. `text/plain` is what sendBeacon sends
// with a string; `application/json` is what fetch and sendBeacon-with-typed-Blob send.
// A body with no Content-Type also passes — sendBeacon with an untyped Blob sends none.
var tiposAceitos = map[string]struct{}{
	"text/plain":       {},
	"application/json": {},
}

type inEvent struct {
	Screen string `json:"screen"`
	Origin string `json:"origin"`
}

type inBatch struct {
	V       int       `json:"v"`
	S       string    `json:"s"`
	E       []inEvent `json:"e"`
	Dropped int       `json:"dropped"`
}

// outRecord is the closed schema written to the JSONL. It is the second
// barrier against free-form fields: even if the decoder let something through,
// only these 6 fields are serialized. No URL, no query string, no screen
// content.
type outRecord struct {
	TS     string `json:"ts"`
	V      int    `json:"v"`
	Fork   string `json:"fork"`
	SID    string `json:"sid"`
	Screen string `json:"screen"`
	Origin string `json:"origin"`
}

// Handler returns the POST /api/telemetry handler.
//
// It has to be registered INSIDE the authenticated group (the `protected` mux,
// behind auth.Middleware). An anonymous route is forbidden — ASVS V3.
//
// The response never has a body: 204 on success, 400 on invalid input,
// 405 on the wrong method, 415 on a Content-Type outside the allowlist. Not
// returning error detail is deliberate — the browser has nothing to do with it
// and a descriptive message would only help whoever is probing the endpoint.
func Handler(sink *Sink, fork string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if ct := r.Header.Get("Content-Type"); ct != "" {
			mt, _, err := mime.ParseMediaType(ct)
			if err != nil {
				w.WriteHeader(http.StatusUnsupportedMediaType)
				return
			}
			if _, ok := tiposAceitos[mt]; !ok {
				w.WriteHeader(http.StatusUnsupportedMediaType)
				return
			}
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxBody)
		var b inBatch
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields() // a free-form field is a 400, never silently ignored
		if err := dec.Decode(&b); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if !reSID.MatchString(b.S) || len(b.E) == 0 || len(b.E) > maxEvents {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		// COMPLETE validation before writing anything: an invalid batch must
		// not leave half a dozen lines written and the rest refused — the
		// usage report would then count use that never happened.
		for _, e := range b.E {
			if e.Origin != "default" && e.Origin != "nav" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
		}

		ts := time.Now().Format(time.RFC3339Nano)
		for _, e := range b.E {
			screen := e.Screen
			if !IsKnownScreen(screen) {
				// The raw id is NEVER written: it would poison the usage report
				// and it is log injection.
				screen = "unknown"
			}
			rec, err := json.Marshal(outRecord{
				TS: ts, V: schemaVer, Fork: fork,
				SID: b.S, Screen: screen, Origin: e.Origin,
			})
			if err != nil {
				continue
			}
			// A sink error does not become a UI error: telemetry never gets in
			// the panel's way (accepted disposition). Losing an event is
			// acceptable; taking the panel down is not.
			_ = sink.Write(rec)
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
