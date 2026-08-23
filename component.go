package component

import (
	"context"
	"sync/atomic"
)

// Node identifies one lifecycle owner of type T in a Registry.
//
// A Node carries no value and provides no lookup capability. Its identity is
// opaque; the label supplied to NewNode is used only for diagnostics. Node
// values must not be copied; copy the pointer returned by NewNode instead.
type Node[T Lifecycle] struct {
	noCopy   noCopy[T]
	identity *nodeIdentity
}

// NodeRef is a type-erased Node pointer used to express lifecycle-ordering
// dependencies between owners of different types. It cannot be implemented
// outside this package.
type NodeRef interface {
	String() string
	nodeIdentity() *nodeIdentity
}

// noCopy marks values as non-copyable for go vet and keeps instantiations for
// different lifecycle types structurally distinct.
type noCopy[T any] struct{}

func (*noCopy[T]) Lock()   {}
func (*noCopy[T]) Unlock() {}

type nodeIdentity struct {
	label   string
	ordinal uint64
}

type nodeDescriptor struct {
	identity *nodeIdentity
	label    string
	ordinal  uint64
}

func descriptor(node NodeRef) nodeDescriptor {
	identity := node.nodeIdentity()
	return nodeDescriptor{
		identity: identity,
		label:    identity.label,
		ordinal:  identity.ordinal,
	}
}

var nextNodeOrdinal atomic.Uint64

// NewNode creates a distinct lifecycle identity. Reusing a label does not
// reuse an identity.
func NewNode[T Lifecycle](label string) *Node[T] {
	return &Node[T]{identity: &nodeIdentity{
		label:   label,
		ordinal: nextNodeOrdinal.Add(1),
	}}
}

// String returns the node's diagnostic label. A nil or zero Node is reported
// as "<invalid>".
func (n *Node[T]) String() string {
	if n == nil || n.identity == nil {
		return "<invalid>"
	}
	return n.identity.label
}

func (n *Node[T]) nodeIdentity() *nodeIdentity {
	if n == nil {
		return nil
	}
	return n.identity
}

// Lifecycle controls one resource owner.
//
// Configure establishes reversible, non-live state. Start makes the owner
// live and returns once it is ready. Stop releases configured or started
// state and must tolerate partial setup and retries after a failed Stop.
// Application-owned types should implement Lifecycle directly.
type Lifecycle interface {
	Configure(ctx context.Context) error
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

// LifecycleFuncs adapts lifecycle functions or external owners with
// incompatible method signatures to Lifecycle. Nil callbacks are no-ops.
type LifecycleFuncs struct {
	OnConfigure func(context.Context) error
	OnStart     func(context.Context) error
	OnStop      func(context.Context) error
}

// Configure invokes OnConfigure when set; otherwise it returns nil.
func (f LifecycleFuncs) Configure(ctx context.Context) error {
	if f.OnConfigure == nil {
		return nil
	}
	return f.OnConfigure(ctx)
}

// Start invokes OnStart when set; otherwise it returns nil.
func (f LifecycleFuncs) Start(ctx context.Context) error {
	if f.OnStart == nil {
		return nil
	}
	return f.OnStart(ctx)
}

// Stop invokes OnStop when set; otherwise it returns nil.
func (f LifecycleFuncs) Stop(ctx context.Context) error {
	if f.OnStop == nil {
		return nil
	}
	return f.OnStop(ctx)
}
