package sdui

import (
	"context"
	"errors"
	"sort"
	"sync"
)

const CurrentSDUIVersion = 1

type Builder func(ctx context.Context, v Viewer) (*Envelope, error)

var ErrScreenNotFound = errors.New("sdui: screen not found")

var (
	registryMu sync.Mutex
	registry   = map[string]Builder{}
)

func Register(id string, b Builder) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registry[id]; exists {
		panic("sdui: screen already registered: " + id)
	}
	registry[id] = b
}

func Build(ctx context.Context, id string, v Viewer) (*Envelope, error) {
	registryMu.Lock()
	b, ok := registry[id]
	registryMu.Unlock()
	if !ok {
		return nil, ErrScreenNotFound
	}
	env, err := b(ctx, v)
	if err != nil {
		return nil, err
	}
	env.SDUIVersion = CurrentSDUIVersion
	return env, nil
}

func RegisteredScreens() []string {
	registryMu.Lock()
	defer registryMu.Unlock()
	ids := make([]string, 0, len(registry))
	for id := range registry {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
