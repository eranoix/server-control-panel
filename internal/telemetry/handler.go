package telemetry

import (
	"encoding/json"
	"mime"
	"net/http"
	"regexp"
	"time"
)

const (
	maxBody   = 64 << 10
	maxEvents = 200
	schemaVer = 1
)

var reSID = regexp.MustCompile(`^[a-f0-9]{8,32}$`)

var acceptedTypes = map[string]struct{}{
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

type outRecord struct {
	TS     string `json:"ts"`
	V      int    `json:"v"`
	Fork   string `json:"fork"`
	SID    string `json:"sid"`
	Screen string `json:"screen"`
	Origin string `json:"origin"`
}

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
			if _, ok := acceptedTypes[mt]; !ok {
				w.WriteHeader(http.StatusUnsupportedMediaType)
				return
			}
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxBody)
		var b inBatch
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&b); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if !reSID.MatchString(b.S) || len(b.E) == 0 || len(b.E) > maxEvents {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

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
				screen = "unknown"
			}
			rec, err := json.Marshal(outRecord{
				TS: ts, V: schemaVer, Fork: fork,
				SID: b.S, Screen: screen, Origin: e.Origin,
			})
			if err != nil {
				continue
			}
			_ = sink.Write(rec)
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
