package inventory

// Nodes the hypervisor does not know about.
//
// Autodiscovery covers the host and the hypervisor's own guests (poller.go).
// What is left over never appears in /cluster/resources: a machine outside the
// hypervisor entirely, a future node running the agent, a server that is simply
// somewhere else. Those arrive as VERSIONED DATA — data/inventory/seeds.json —
// rather than as code.
//
// # Why data, and not a list in the source
//
// A list in the source would mean recompiling and redeploying the panel just to
// declare a node, and it would put real addresses in the discovery code. As a
// file it is versioned alongside the rest of the infrastructure, reviewable as
// a diff, and changeable without a deploy.
//
// # Absent is legitimate; malformed is NOT
//
// An absent file means "no nodes outside the hypervisor" — the normal state of
// a fresh install, and returning an error there would make the panel refuse to
// start over an optional file. A file that is PRESENT and unreadable is a hard
// error: accepting it in silence would let an operator believe they declared a
// node the panel never read. Same posture as internal/config/config_io.go.
//
// # The canary does not live here
//
// The permanently dead node — the one that proves staleness grows and the alarm
// fires — is an ORDINARY seed created at runtime. Not a line of this file knows
// about it, and that is the point: if "canary" were a feature, the proof would
// be circular, with the code that invents a dead node proving it can detect a
// dead node.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// SeedsFileName is the file read from inside the panel's data directory.
const SeedsFileName = "seeds.json"

// SeedsPath returns the path of the seeds file inside a data directory.
func SeedsPath(dataDir string) string {
	return filepath.Join(dataDir, "inventory", SeedsFileName)
}

// LoadSeeds reads the declared nodes. Absent means zero seeds and no error.
// Present but malformed, or carrying an invalid node, is a hard error that
// QUOTES the offending value — a wrong transport that becomes a "generic error"
// in the log is a defect nobody ever finds.
//
// Validation is the SAME as the model's (Node.Validate): a seed may not enter
// through a looser door than discovery does. If the transport enum is closed
// for the hypervisor, it is closed for the file too.
func LoadSeeds(dataDir string) ([]Node, error) {
	path := SeedsPath(dataDir)
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("seeds: read %s: %w", path, err)
	}

	var seeds []Node
	if err := json.Unmarshal(raw, &seeds); err != nil {
		return nil, fmt.Errorf("seeds: %s malformed: %w", path, err)
	}

	vistos := make(map[string]bool, len(seeds))
	for i, s := range seeds {
		if err := s.Validate(); err != nil {
			return nil, fmt.Errorf("seeds: %s, entry %d: %w", path, i, err)
		}
		if vistos[s.ID] {
			// Two seeds with the same ID would merge nodes silently — the last
			// one would win and the operator would never learn which was
			// dropped.
			return nil, fmt.Errorf("seeds: %s, entry %d: duplicate ID %q", path, i, s.ID)
		}
		vistos[s.ID] = true
		if s.Transport == TransportAgente && s.Address == "" {
			// A transport that requires active polling, with no address, is a
			// node that will never be observed, presented as though it were.
			return nil, fmt.Errorf("seeds: %s, entry %d: node %q with transport agente requires address", path, i, s.ID)
		}
	}
	return seeds, nil
}

// SeedsSource wraps LoadSeeds for the poller's Sources, re-reading the file on
// every tick. Re-reading is deliberate: editing seeds.json takes effect on the
// next tick, with no restart.
func SeedsSource(dataDir string) func() ([]Node, error) {
	return func() ([]Node, error) { return LoadSeeds(dataDir) }
}
