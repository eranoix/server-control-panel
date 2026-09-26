package telemetry

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestProofExternalFile exists only to produce a REAL file outside t.TempDir(),
// so it can be checked by python3 from outside Go (the second acceptance
// criterion). Runs only when TELEMETRY_PROOF_DIR is set.
func TestProofExternalFile(t *testing.T) {
	dir := os.Getenv("TELEMETRY_PROOF_DIR")
	if dir == "" {
		t.Skip("TELEMETRY_PROOF_DIR not set")
	}
	s, err := NewSink(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	tm, _ := time.Parse("2006-01-02", "2026-08-06")
	s.now = func() time.Time { return tm }

	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rec, _ := json.Marshal(map[string]any{"i": i, "pad": strings.Repeat("p", 200)})
			if err := s.Write(rec); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
}
