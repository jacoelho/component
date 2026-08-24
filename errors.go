package component

import "errors"

var (
	// ErrInvalidNode reports a nil or zero Node or NodeRef in a declaration.
	ErrInvalidNode = errors.New("component: invalid node")
	// ErrInvalidLifecycle reports a nil or typed-nil Lifecycle.
	ErrInvalidLifecycle = errors.New("component: invalid lifecycle")
	// ErrAlreadyRegistered reports a second declaration for the same Node.
	ErrAlreadyRegistered = errors.New("component: node already registered")
	// ErrDuplicateDependency reports a repeated dependency edge.
	ErrDuplicateDependency = errors.New("component: duplicate dependency")
	// ErrNotRegistered reports a dependency without a declaration.
	ErrNotRegistered = errors.New("component: node not registered")
	// ErrCyclicDependency reports a dependency cycle.
	ErrCyclicDependency = errors.New("component: cyclic dependency")
	// ErrRegistryConsumed reports use after Compile accepted the graph and
	// began construction.
	ErrRegistryConsumed = errors.New("component: registry consumed")
	// ErrInvalidConstructor reports an unsupported provider constructor.
	ErrInvalidConstructor = errors.New("component: invalid constructor")
	// ErrInvalidBinding reports an invalid or repeated explicit type binding.
	ErrInvalidBinding = errors.New("component: invalid binding")
	// ErrAmbiguousDependency reports multiple owners matching one constructor
	// parameter.
	ErrAmbiguousDependency = errors.New("component: ambiguous dependency")
	// ErrConstruction reports a provider constructor error, panic, abort, or
	// invalid lifecycle result.
	ErrConstruction = errors.New("component: construction failed")
	// ErrAlreadyStarted reports a Start call on a one-shot Runtime that has
	// already had a Start attempt or has been stopped.
	ErrAlreadyStarted = errors.New("component: runtime already started")
	// ErrRuntimeBusy reports an overlapping lifecycle operation.
	ErrRuntimeBusy = errors.New("component: runtime busy")
	// ErrCleanupPending reports a Start attempt while cleanup from a failed
	// Start or Stop remains.
	ErrCleanupPending = errors.New("component: cleanup pending")
	// ErrPanic reports a panic raised by a lifecycle callback.
	ErrPanic = errors.New("component: lifecycle panic")
	// ErrLifecycleAborted reports a lifecycle callback that called
	// runtime.Goexit.
	ErrLifecycleAborted = errors.New("component: lifecycle aborted")
)
