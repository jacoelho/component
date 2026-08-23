package component

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

//nolint:iface,ireturn // Test nodes intentionally exercise the Lifecycle interface type.
func noOpLifecycle() Lifecycle {
	return LifecycleFuncs{}
}

type testNode = *Node[Lifecycle]

func newTestNode(label string) testNode {
	return NewNode[Lifecycle](label)
}

func TestNodeIdentity(t *testing.T) {
	t.Parallel()

	var zero testNode
	if got := zero.String(); got != "<invalid>" {
		t.Fatalf("zero Node String() = %q, want %q", got, "<invalid>")
	}

	first := newTestNode("worker")
	second := newTestNode("worker")
	copyOfFirst := first
	if first == second {
		t.Fatal("nodes created with the same label share identity")
	}
	if first != copyOfFirst {
		t.Fatal("copying a Node pointer did not preserve identity")
	}
	if first.String() != "worker" || second.String() != "worker" {
		t.Fatalf("diagnostic labels changed: %q, %q", first, second)
	}

	unnamed := newTestNode("")
	if got := unnamed.String(); got != "" {
		t.Fatalf("unnamed Node String() = %q, want empty label", got)
	}
}

func TestLifecycleFuncsNilCallbacksAreNoOps(t *testing.T) {
	t.Parallel()

	lifecycle := LifecycleFuncs{}
	ctx := context.Background()
	for phase, invoke := range map[string]func(context.Context) error{
		"configure": lifecycle.Configure,
		"start":     lifecycle.Start,
		"stop":      lifecycle.Stop,
	} {
		if err := invoke(ctx); err != nil {
			t.Errorf("%s returned %v", phase, err)
		}
	}
}

func TestZeroRegistryIsUsable(t *testing.T) {
	t.Parallel()

	var configureCalls atomic.Int32
	var startCalls atomic.Int32
	var stopCalls atomic.Int32
	var registry Registry

	if err := registry.Register(NewNode[LifecycleFuncs]("worker"), LifecycleFuncs{
		OnConfigure: func(context.Context) error {
			configureCalls.Add(1)
			return nil
		},
		OnStart: func(context.Context) error {
			startCalls.Add(1)
			return nil
		},
		OnStop: func(context.Context) error {
			stopCalls.Add(1)
			return nil
		},
	}); err != nil {
		t.Fatalf("Register() failed: %v", err)
	}

	runtime, err := registry.Compile()
	if err != nil {
		t.Fatalf("Compile() failed: %v", err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() failed: %v", err)
	}

	if got := configureCalls.Load(); got != 1 {
		t.Fatalf("Configure calls = %d, want 1", got)
	}
	if got := startCalls.Load(); got != 1 {
		t.Fatalf("Start calls = %d, want 1", got)
	}
	if got := stopCalls.Load(); got != 1 {
		t.Fatalf("Stop calls = %d, want 1", got)
	}
}

func TestZeroRegistryConcurrentFirstUseSharesCore(t *testing.T) {
	t.Parallel()

	const registrationCount = 32
	nodes := make([]testNode, registrationCount)
	for index := range nodes {
		nodes[index] = newTestNode("worker")
	}

	var registry Registry
	start := make(chan struct{})
	results := make(chan error, registrationCount)
	for _, node := range nodes {
		go func() {
			<-start
			results <- registry.Register(node, noOpLifecycle())
		}()
	}
	close(start)

	for range nodes {
		if err := <-results; err != nil {
			t.Fatalf("concurrent Register() failed: %v", err)
		}
	}

	runtime, err := registry.Compile()
	if err != nil {
		t.Fatalf("Compile() failed: %v", err)
	}
	if got := len(runtime.core.entries); got != registrationCount {
		t.Fatalf("compiled entry count = %d, want %d", got, registrationCount)
	}
}

func BenchmarkParallelRegistryConstruction(b *testing.B) {
	node := newTestNode("worker")
	lifecycle := noOpLifecycle()
	b.ReportAllocs()
	b.RunParallel(func(iterations *testing.PB) {
		for iterations.Next() {
			registry := NewRegistry()
			if err := registry.Register(node, lifecycle); err != nil {
				b.Errorf("Register() failed: %v", err)
				return
			}
		}
	})
}

type nilLifecycle struct{}

func (*nilLifecycle) Configure(context.Context) error { return nil }
func (*nilLifecycle) Start(context.Context) error     { return nil }
func (*nilLifecycle) Stop(context.Context) error      { return nil }

func TestRegistryAcceptsHeterogeneousTypedDependencies(t *testing.T) {
	t.Parallel()

	adapterDependency := NewNode[LifecycleFuncs]("adapter")
	pointerDependency := NewNode[*nilLifecycle]("pointer")
	owner := NewNode[LifecycleFuncs]("owner")
	registry := NewRegistry()
	if err := registry.Register(adapterDependency, LifecycleFuncs{}); err != nil {
		t.Fatalf("Register(adapter) failed: %v", err)
	}
	if err := registry.Register(pointerDependency, &nilLifecycle{}); err != nil {
		t.Fatalf("Register(pointer) failed: %v", err)
	}
	dependencies := []NodeRef{adapterDependency, pointerDependency}
	if err := registry.Register(owner, LifecycleFuncs{}, dependencies...); err != nil {
		t.Fatalf("Register(owner) failed: %v", err)
	}
	if _, err := registry.Compile(); err != nil {
		t.Fatalf("Compile() failed: %v", err)
	}
}

func TestRegistryRejectsInvalidDeclarations(t *testing.T) {
	t.Parallel()

	validNode := newTestNode("valid")
	dependency := newTestNode("dependency")
	var typedNil *nilLifecycle
	var typedNilDependency testNode

	tests := []struct {
		name      string
		node      testNode
		lifecycle Lifecycle
		deps      []NodeRef
		want      error
	}{
		{
			name:      "zero owner",
			lifecycle: noOpLifecycle(),
			want:      ErrInvalidNode,
		},
		{
			name: "nil lifecycle",
			node: validNode,
			want: ErrInvalidLifecycle,
		},
		{
			name:      "typed nil lifecycle",
			node:      validNode,
			lifecycle: typedNil,
			want:      ErrInvalidLifecycle,
		},
		{
			name:      "zero dependency",
			node:      validNode,
			lifecycle: noOpLifecycle(),
			deps:      []NodeRef{new(Node[Lifecycle])},
			want:      ErrInvalidNode,
		},
		{
			name:      "nil dependency reference",
			node:      validNode,
			lifecycle: noOpLifecycle(),
			deps:      []NodeRef{nil},
			want:      ErrInvalidNode,
		},
		{
			name:      "typed nil dependency reference",
			node:      validNode,
			lifecycle: noOpLifecycle(),
			deps:      []NodeRef{typedNilDependency},
			want:      ErrInvalidNode,
		},
		{
			name:      "duplicate edge",
			node:      validNode,
			lifecycle: noOpLifecycle(),
			deps:      []NodeRef{dependency, dependency},
			want:      ErrDuplicateDependency,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var registry Registry
			err := registry.Register(test.node, test.lifecycle, test.deps...)
			if !errors.Is(err, test.want) {
				t.Fatalf("Register() error = %v, want errors.Is(_, %v)", err, test.want)
			}
		})
	}
}

func TestRegistryRejectsDuplicateRegistration(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	node := newTestNode("worker")
	if err := registry.Register(node, noOpLifecycle()); err != nil {
		t.Fatalf("first Register() failed: %v", err)
	}
	if err := registry.Register(node, noOpLifecycle()); !errors.Is(err, ErrAlreadyRegistered) {
		t.Fatalf("second Register() error = %v, want ErrAlreadyRegistered", err)
	}
}

func TestCompileFailureLeavesRegistryEditable(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	dependency := newTestNode("dependency")
	dependent := newTestNode("dependent")
	if err := registry.Register(dependent, noOpLifecycle(), dependency); err != nil {
		t.Fatalf("Register(dependent) failed: %v", err)
	}

	if _, err := registry.Compile(); !errors.Is(err, ErrNotRegistered) {
		t.Fatalf("Compile() error = %v, want ErrNotRegistered", err)
	}
	if err := registry.Register(dependency, noOpLifecycle()); err != nil {
		t.Fatalf("Register(dependency) after failed Compile() failed: %v", err)
	}
	if _, err := registry.Compile(); err != nil {
		t.Fatalf("Compile() after repair failed: %v", err)
	}
}

func TestSuccessfulCompileConsumesRegistry(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	node := newTestNode("worker")
	if err := registry.Register(node, noOpLifecycle()); err != nil {
		t.Fatalf("Register() failed: %v", err)
	}
	runtime, err := registry.Compile()
	if err != nil {
		t.Fatalf("Compile() failed: %v", err)
	}
	if runtime == nil {
		t.Fatal("Compile() returned a nil Runtime")
	}
	if err := registry.Register(newTestNode("late"), noOpLifecycle()); !errors.Is(err, ErrRegistryConsumed) {
		t.Fatalf("Register() after Compile() error = %v, want ErrRegistryConsumed", err)
	}
	if _, err := registry.Compile(); !errors.Is(err, ErrRegistryConsumed) {
		t.Fatalf("second Compile() error = %v, want ErrRegistryConsumed", err)
	}
}

func TestCopiedRegistrySharesConsumptionState(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	if err := registry.Register(newTestNode("worker"), noOpLifecycle()); err != nil {
		t.Fatalf("Register() failed: %v", err)
	}
	copied := *registry
	results := make(chan error, 2)
	go func() {
		_, err := copied.Compile()
		results <- err
	}()
	go func() {
		_, err := registry.Compile()
		results <- err
	}()

	successes := 0
	consumed := 0
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrRegistryConsumed):
			consumed++
		default:
			t.Fatalf("Compile() through registry copy returned %v", err)
		}
	}
	if successes != 1 || consumed != 1 {
		t.Fatalf("Compile() results: successes=%d consumed=%d, want 1 and 1", successes, consumed)
	}
}
