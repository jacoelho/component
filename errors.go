package component

import (
	"errors"
	"fmt"
)

var (
	// ErrInvalidDefinition reports an invalid factory or lifecycle declaration.
	ErrInvalidDefinition = errors.New("component: invalid definition")
	// ErrInvalidReference reports an absent, zero, or foreign reference.
	ErrInvalidReference = errors.New("component: invalid reference")
	// ErrInvalidValue reports a successful managed factory returning nil.
	ErrInvalidValue = errors.New("component: invalid managed value")
	// ErrInvalidRuntime reports a nil or zero runtime.
	ErrInvalidRuntime = errors.New("component: invalid runtime")
	// ErrInvalidOptions reports invalid runtime limits.
	ErrInvalidOptions = errors.New("component: invalid options")
	// ErrInvalidContext reports a nil operation context.
	ErrInvalidContext = errors.New("component: invalid context")
	// ErrBusy reports an overlapping startup or shutdown operation.
	ErrBusy = errors.New("component: runtime busy")
	// ErrAlreadyStarted reports reuse of the one startup attempt.
	ErrAlreadyStarted = errors.New("component: runtime already started")
	// ErrUnavailable reports value access outside the running state.
	ErrUnavailable = errors.New("component: value unavailable")
	// ErrCleanupPending reports incomplete cleanup after a Stop call.
	ErrCleanupPending = errors.New("component: cleanup pending")
	// ErrPanic reports a callback panic; the original error cause remains inspectable.
	ErrPanic = errors.New("component: callback panic")
	// ErrAborted reports a callback terminating through runtime.Goexit.
	ErrAborted = errors.New("component: callback aborted")
)

// NodeError locates a failure within a runtime. IDs are one-based root/input
// preorder. Phase is declare, construct, start, or stop. Stack is set on panic.
// Causes are kept opaque until the caller formats or inspects the error.
type NodeError struct {
	ID         int
	Name       string
	Phase      string
	Cause      error
	Stack      []byte
	kind       error
	panicValue any
}

func (e *NodeError) Error() string {
	text := fmt.Sprintf("component: node %d %q %s", e.ID, e.Name, e.Phase)
	if e.kind != nil {
		text += ": " + e.kind.Error()
	}
	if e.Cause != nil {
		text += ": " + e.Cause.Error()
	}
	if e.Cause == nil && e.panicValue != nil {
		text += ": " + fmt.Sprint(e.panicValue)
	}
	if len(e.Stack) != 0 {
		text += "\n" + string(e.Stack)
	}
	return text
}

// Unwrap preserves both invocation classification and the original cause.
func (e *NodeError) Unwrap() []error {
	if e.kind == nil {
		if e.Cause == nil {
			return nil
		}
		return []error{e.Cause}
	}
	if e.Cause == nil {
		return []error{e.kind}
	}
	return []error{e.kind, e.Cause}
}

func nodeFailure(index int, node *definition, phase string, cause error) *NodeError {
	name := node.name
	if name == "" {
		name = node.declaredType.String()
	}
	return &NodeError{ID: index + 1, Name: name, Phase: phase, Cause: cause}
}
