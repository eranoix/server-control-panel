package gameservers

import (
	"context"
	"encoding/json"
	"io"
)

type Handle string

type Verb string

const (
	VerbStart   Verb = "start"
	VerbStop    Verb = "stop"
	VerbRestart Verb = "restart"
	VerbUpdate  Verb = "update"
)

var ValidVerbs = map[Verb]bool{
	VerbStart: true, VerbStop: true, VerbRestart: true, VerbUpdate: true,
}

type Backend interface {
	Execute(ctx context.Context, op OpName, body json.RawMessage) (json.RawMessage, error)

	Open(ctx context.Context, h Handle) (io.ReadCloser, error)

	Receive(ctx context.Context, r io.Reader) (Handle, error)

	Describe() string
}

type errorHandle string

const (
	ErrHandleInvalid errorHandle = "handle is invalid, expired or from another server"

	ErrTrainerMissing errorHandle = "trainer is not installed on this machine"
)

func (e errorHandle) Error() string { return string(e) }

type AuthorizationError struct{ Msg string }

func (e *AuthorizationError) Error() string { return e.Msg }

type UnknownOperationError struct{ Op OpName }

func (e *UnknownOperationError) Error() string {
	return "unknown operation on node: " + string(e.Op)
}

type OperationError struct{ Msg string }

func (e *OperationError) Error() string { return e.Msg }
