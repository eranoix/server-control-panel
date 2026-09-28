package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

type SessionBackupArgs struct {
	Owner     string `json:"owner"`
	Session   string `json:"session,omitempty"`
	Retention int    `json:"retention,omitempty"`
}

type SessionBackupRunner struct {
	Backup func(ctx context.Context, owner, session string, retention int, logW io.Writer) error
}

func (SessionBackupRunner) Kind() string { return "session_backup" }

func (SessionBackupRunner) AuthorizedFor(string, bool) bool { return true }

func (t SessionBackupRunner) Run(ctx context.Context, args json.RawMessage, logW io.Writer, progress func(int), step func(string)) error {
	var a SessionBackupArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return fmt.Errorf("args: %w", err)
	}
	if a.Owner == "" {
		return errors.New("owner missing from the schedule (recreate the session backup)")
	}
	if t.Backup == nil {
		return errors.New("session backup unavailable")
	}
	label := a.Session
	if label == "" || label == "all" {
		label = "all your sessions"
	}
	step("backing up " + label)
	progress(10)
	if err := t.Backup(ctx, a.Owner, a.Session, a.Retention, logW); err != nil {
		return err
	}
	progress(100)
	step("done")
	return nil
}
