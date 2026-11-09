package component

import (
	"errors"
	"fmt"
)

var (
	// ErrInvalidDefinition reports a nil constructor or hook, or an invalid
	// Option. New returns it before any constructor runs.
	ErrInvalidDefinition = errors.New("component: invalid definition")
	// ErrInvalidReference reports a zero or nil Ref, or a Replace whose Ref
	// is not reachable from the roots or is replaced twice.
	ErrInvalidReference = errors.New("component: invalid reference")
	// ErrBuildFailed reports use of an App whose New returned an error. Stop
	// is the only call that still does work on it.
	ErrBuildFailed = errors.New("component: build failed")
	// ErrStarted reports a second call to Start.
	ErrStarted = errors.New("component: already started")
	// ErrStopped reports use of an App after Stop began.
	ErrStopped = errors.New("component: stopped")
	// ErrExited reports a Go task that returned nil before Stop began.
	ErrExited = errors.New("component: task exited")
	// ErrAbandoned reports a Go task still running when the stop context
	// ended.
	ErrAbandoned = errors.New("component: task abandoned")
	// ErrPanic reports a constructor, hook or task that panicked. The error
	// is a *PanicError.
	ErrPanic = errors.New("component: panic")
)

// Phase is the part of the lifecycle in which a node failed.
type Phase string

// Phases reported in NodeError.
const (
	PhaseBuild Phase = "build" // a constructor, or an invalid declaration
	PhaseStart Phase = "start" // an OnStart hook
	PhaseTask  Phase = "task"  // a Go task
	PhaseDrain Phase = "drain" // an OnDrain hook
	PhaseStop  Phase = "stop"  // an OnStop hook, or a task abandoned by Stop
	PhaseClose Phase = "close" // an OnClose hook
)

// NodeError locates a failure: the node's label and the phase that failed.
type NodeError struct {
	Err   error
	Label string
	Phase Phase
}

func (e *NodeError) Error() string {
	return fmt.Sprintf("component: %s: %s: %v", e.Label, e.Phase, e.Err)
}

func (e *NodeError) Unwrap() error { return e.Err }

// PanicError carries a recovered panic and the stack where it happened.
// errors.Is(err, ErrPanic) matches it, and a panic value that is an error
// stays reachable through errors.Is and errors.As.
type PanicError struct {
	Value any
	Stack []byte
}

func (e *PanicError) Error() string {
	return fmt.Sprintf("%v: %v\n%s", ErrPanic, e.Value, e.Stack)
}

func (e *PanicError) Unwrap() []error {
	if err, ok := e.Value.(error); ok {
		return []error{ErrPanic, err}
	}
	return []error{ErrPanic}
}
