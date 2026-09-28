package gameservers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

const trainerBin = "/usr/local/bin/tl-agent"

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

func (m *Manager) TrainerAvailable() bool {
	_, err := exec.LookPath(trainerBin)
	return err == nil
}

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

func (m *Manager) TrainerApply(ctx context.Context) (string, error) {
	b, err := trainerRun(ctx, nil, "apply")
	if err != nil {
		return "", err
	}
	return string(bytes.TrimSpace(b)), nil
}
