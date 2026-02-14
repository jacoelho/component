package component

import (
	"errors"
	"fmt"
)

var (
	// ErrCyclicDependency is returned by Provide/Compile when a dependency cycle is detected.
	ErrCyclicDependency = errors.New("cyclic dependency")

	// ErrNotRegistered is returned by Compile/Get when a referenced component key
	// has not been registered.
	ErrNotRegistered = errors.New("not registered")

	// ErrAlreadyRegistered is returned by Provide when a component key is reused.
	ErrAlreadyRegistered = errors.New("already registered")

	// ErrNotStarted is returned by Get when the component instance is unavailable.
	ErrNotStarted = errors.New("not started")

	// ErrIncorrectType is returned by Get when the stored instance cannot be asserted to T.
	ErrIncorrectType = errors.New("incorrect type")

	// ErrAlreadyInitialized is returned when multiple component initializations are attempted.
	ErrAlreadyInitialized = errors.New("already initialized")

	// ErrPanic is returned when constructor/start/stop panics are recovered.
	ErrPanic = errors.New("component panicked")

	// ErrAlreadyStarted is returned when Start is called while already started.
	ErrAlreadyStarted = errors.New("already started")

	// ErrInvalidStateTransition is returned when Start/Stop is called from an invalid state.
	ErrInvalidStateTransition = errors.New("invalid state transition")

	// ErrNilPlan is returned when trying to create a runtime from a nil plan.
	ErrNilPlan = errors.New("nil plan")
)

func wrapComponentError(componentID string, operation string, err error) error {
	return fmt.Errorf("component %q %s: %w", componentID, operation, err)
}

func wrapRegistrationError(componentID string, err error) error {
	return fmt.Errorf("component %q already registered: %w", componentID, err)
}

func wrapRetrievalError(componentID string, err error) error {
	return fmt.Errorf("component %q not registered: %w", componentID, err)
}
