package inventory

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const SeedsFileName = "seeds.json"

func SeedsPath(dataDir string) string {
	return filepath.Join(dataDir, "inventory", SeedsFileName)
}

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

	seen := make(map[string]bool, len(seeds))
	for i, s := range seeds {
		if err := s.Validate(); err != nil {
			return nil, fmt.Errorf("seeds: %s, entry %d: %w", path, i, err)
		}
		if seen[s.ID] {
			return nil, fmt.Errorf("seeds: %s, entry %d: duplicate ID %q", path, i, s.ID)
		}
		seen[s.ID] = true
		if s.Transport == TransportAgent && s.Address == "" {
			return nil, fmt.Errorf("seeds: %s, entry %d: node %q with the agent transport requires an address", path, i, s.ID)
		}
	}
	return seeds, nil
}

func SeedsSource(dataDir string) func() ([]Node, error) {
	return func() ([]Node, error) { return LoadSeeds(dataDir) }
}
