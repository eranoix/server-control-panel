package gameservers

// Bridge to tl-agent — the trainer for the Enshrouded dedicated server.
//
// Deliberately thin: ALL the intelligence (catalog, policy about who is allowed
// to turn things on, reconciliation with the process memory) lives in tl-agent,
// which is also what runs from the daemon and from the CLI. Duplicating the
// policy here would create two versions of the "only the owner cheats" rule,
// and one day they would drift apart.
//
// The panel only: reads a ready-made snapshot, and writes the desired state.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

const trainerBin = "/usr/local/bin/tl-agent"

// TrainerDesired is what the user asked for — not necessarily what is actually
// applied. The screen shows the two separately on purpose: a toggle reading
// "on" while the memory is still untouched would be a lie.
type TrainerDesired struct {
	Enabled bool               `json:"enabled"`
	Toggles []string           `json:"toggles"`
	Values  map[string]float64 `json:"values"`
}

func trainerRun(ctx context.Context, stdin []byte, args ...string) ([]byte, error) {
	c, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(c, trainerBin, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		msg := errb.String()
		if msg == "" {
			msg = out.String()
		}
		return nil, fmt.Errorf("tl-agent %v: %v (%s)", args, err, msg)
	}
	return out.Bytes(), nil
}

// TrainerAvailable says whether the trainer exists on this machine. Without it
// the tab shows up broken instead of simply not showing up.
func (m *Manager) TrainerAvailable() bool {
	_, err := exec.LookPath(trainerBin)
	return err == nil
}

// TrainerSnapshot returns catalog + desired + applied + policy, raw, so the
// frontend can draw it without recomputing anything.
func (m *Manager) TrainerSnapshot(ctx context.Context) (json.RawMessage, error) {
	b, err := trainerRun(ctx, nil, "json")
	if err != nil {
		return nil, err
	}
	if !json.Valid(b) {
		return nil, fmt.Errorf("tl-agent json returned an invalid payload")
	}
	return json.RawMessage(b), nil
}

// TrainerSetDesired writes what the user asked for and reconciles right away.
// tl-agent filters out unknown ids — the UI cannot write garbage.
func (m *Manager) TrainerSetDesired(ctx context.Context, d TrainerDesired) (json.RawMessage, error) {
	body, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	b, err := trainerRun(ctx, body, "desired")
	if err != nil {
		return nil, err
	}
	if !json.Valid(b) {
		return nil, fmt.Errorf("tl-agent desired returned an invalid payload")
	}
	return json.RawMessage(b), nil
}

// TrainerApply reconciles without changing the desired state — the "reapply
// now" button, useful after a server restart when the daemon is not installed.
func (m *Manager) TrainerApply(ctx context.Context) (string, error) {
	b, err := trainerRun(ctx, nil, "apply")
	if err != nil {
		return "", err
	}
	return string(bytes.TrimSpace(b)), nil
}
