package component_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/jacoelho/component"
)

type eventCollector struct {
	mu     sync.Mutex
	events []string
}

func (ec *eventCollector) Record(componentName, action string) {
	ec.mu.Lock()
	defer ec.mu.Unlock()
	event := fmt.Sprintf("%s:%s", componentName, action)
	ec.events = append(ec.events, event)
}

func (ec *eventCollector) Events() []string {
	ec.mu.Lock()
	defer ec.mu.Unlock()
	return slices.Clone(ec.events)
}

func (ec *eventCollector) Clear() {
	ec.mu.Lock()
	defer ec.mu.Unlock()
	ec.events = make([]string, 0)
}

// stubComponent implements Lifecycle and records events.
type stubComponent struct {
	name      string
	collector *eventCollector
	startErr  error
	stopErr   error
}

func newStub(name string, collector *eventCollector) *stubComponent {
	return &stubComponent{
		name:      name,
		collector: collector,
	}
}

func (s *stubComponent) Start(_ context.Context) error {
	s.collector.Record(s.name, "start")
	return s.startErr
}

func (s *stubComponent) Stop(_ context.Context) error {
	s.collector.Record(s.name, "stop")
	return s.stopErr
}

func TestStartupAndShutdownOrder(t *testing.T) {
	ctx := context.Background()
	sys := new(component.System)
	collector := new(eventCollector)

	aKey := component.NewKey[*stubComponent]("A")
	bKey := component.NewKey[*stubComponent]("B")
	cKey := component.NewKey[*stubComponent]("C")

	mustProvide(t, sys, aKey, func(_ *component.System) (*stubComponent, error) {
		return newStub("A", collector), nil
	})
	mustProvide(t, sys, bKey, func(_ *component.System) (*stubComponent, error) {
		return newStub("B", collector), nil
	})
	mustProvide(t, sys, cKey, func(s *component.System) (*stubComponent, error) {
		a, err := component.Get(s, aKey)
		if err != nil {
			return nil, err
		}
		b, err := component.Get(s, bKey)
		if err != nil {
			return nil, err
		}
		_ = a
		_ = b
		return newStub("C", collector), nil
	}, aKey, bKey)

	if err := sys.Start(ctx); err != nil {
		t.Fatal("Start:", err)
	}

	events := collector.Events()

	// Check that A:start and B:start both happen before C:start
	idxA := slices.Index(events, "A:start")
	idxB := slices.Index(events, "B:start")
	idxC := slices.Index(events, "C:start")

	if idxA < 0 || idxB < 0 || idxC < 0 {
		t.Fatalf("missing start events, events: %v", events)
	}
	if idxA > idxC || idxB > idxC {
		t.Errorf("level-0 must start before C; got order: %v", events)
	}

	if err := sys.Stop(ctx); err != nil {
		t.Fatal("Stop:", err)
	}

	events = collector.Events()

	// Check that C:stop happens before A:stop and B:stop
	idxCStop := slices.Index(events, "C:stop")
	idxAStop := slices.Index(events, "A:stop")
	idxBStop := slices.Index(events, "B:stop")
	if idxCStop < 0 || idxAStop < 0 || idxBStop < 0 {
		t.Fatalf("missing stop events, events: %v", events)
	}
	if idxCStop > idxAStop || idxCStop > idxBStop {
		t.Errorf("C must stop before level-0; got order: %v", events)
	}
}

func TestCycleDetectionDirect(t *testing.T) {
	sys := new(component.System)
	collector := new(eventCollector)

	aKey := component.NewKey[*stubComponent]("A")
	bKey := component.NewKey[*stubComponent]("B")

	// A depends on B
	mustProvide(t, sys, aKey, func(_ *component.System) (*stubComponent, error) {
		return newStub("A", collector), nil
	}, bKey)

	// B depends on A -> cycle
	err := component.Provide(sys, bKey, func(_ *component.System) (*stubComponent, error) {
		return newStub("B", collector), nil
	}, aKey)
	if err == nil {
		t.Fatal("expected cycle detection when providing B")
	}
	if want := component.ErrCyclicDependency; !errors.Is(err, want) {
		t.Fatalf("got error %v, want %v", err, want)
	}
}

func TestCycleDetectionIndirect(t *testing.T) {
	sys := new(component.System)
	collector := new(eventCollector)

	aKey := component.NewKey[*stubComponent]("A")
	bKey := component.NewKey[*stubComponent]("B")
	cKey := component.NewKey[*stubComponent]("C")

	// A depends on B
	mustProvide(t, sys, aKey, func(_ *component.System) (*stubComponent, error) {
		return newStub("A", collector), nil
	}, bKey)

	// B depends on C
	mustProvide(t, sys, bKey, func(_ *component.System) (*stubComponent, error) {
		return newStub("B", collector), nil
	}, cKey)

	// C depends on A --> cycle
	err := component.Provide(sys, cKey, func(_ *component.System) (*stubComponent, error) {
		return newStub("C", collector), nil
	}, aKey)
	if err == nil {
		t.Fatal("expected cycle detection when providing C")
	}
	if want := component.ErrCyclicDependency; !errors.Is(err, want) {
		t.Fatalf("got error %v, want %v", err, want)
	}
}

func TestMissingDependencyErrorOnStart(t *testing.T) {
	sys := new(component.System)
	collector := new(eventCollector)

	aKey := component.NewKey[*stubComponent]("A")
	bKey := component.NewKey[*stubComponent]("B")

	// A depends on B, but B not provided
	mustProvide(t, sys, aKey, func(_ *component.System) (*stubComponent, error) {
		return newStub("A", collector), nil
	}, bKey)

	// Start should error about missing dependency B
	err := sys.Start(context.Background())
	if err == nil {
		t.Fatal("expected error on Start with missing dependency")
	}
	if want := component.ErrNotRegistered; !errors.Is(err, want) {
		t.Fatalf("expected error %v, got %v", want, err)
	}

	// Check that "A:start" was not recorded, as its dependency was missing.
	if events := collector.Events(); slices.Contains(events, "A:start") {
		t.Errorf("A:start should not have been recorded due to missing dependency, events: %v", events)
	}
}

func TestProvideErrAlreadyRegistered(t *testing.T) {
	sys := new(component.System)
	collector := new(eventCollector)

	aKey := component.NewKey[*stubComponent]("A")

	mustProvide(t, sys, aKey, func(_ *component.System) (*stubComponent, error) {
		return newStub("A1", collector), nil
	})

	err := component.Provide(sys, aKey, func(_ *component.System) (*stubComponent, error) {
		return newStub("A2", collector), nil
	})

	if want := component.ErrAlreadyRegistered; !errors.Is(err, want) {
		t.Fatalf("expected error %v, got %v", want, err)
	}
}

func TestGet(t *testing.T) {
	sys := new(component.System)
	collector := new(eventCollector)
	ctx := context.Background()

	aKey := component.NewKey[*stubComponent]("A")
	bKey := component.NewKey[*stubComponent]("B") // Not registered

	// Provide A
	mustProvide(t, sys, aKey, func(_ *component.System) (*stubComponent, error) {
		return newStub("A", collector), nil
	})

	// 1. Get before Start
	_, err := component.Get(sys, aKey)
	if !errors.Is(err, component.ErrNotStarted) {
		t.Errorf("expected ErrNotStarted when getting before Start, got %v", err)
	}

	// Start the system
	if err := sys.Start(ctx); err != nil {
		t.Fatalf("sys.Start failed: %v", err)
	}

	// 2. Get unregistered component (after system start)
	_, err = component.Get(sys, bKey)
	if !errors.Is(err, component.ErrNotRegistered) {
		t.Errorf("expected ErrNotRegistered for bKey, got %v", err)
	}

	// 3. Get successfully
	compA, err := component.Get(sys, aKey)
	if err != nil {
		t.Fatalf("component.Get(aKey) failed: %v", err)
	}
	if compA.name != "A" {
		t.Errorf("expected component A, got %s", compA.name)
	}

	if err := sys.Stop(ctx); err != nil {
		t.Fatalf("sys.Stop failed: %v", err)
	}
}

func mustProvide[T component.Lifecycle](
	t *testing.T,
	sys *component.System,
	key component.Key[T],
	fn func(*component.System) (T, error),
	deps ...component.Keyer,
) {
	t.Helper()
	if err := component.Provide(sys, key, fn, deps...); err != nil {
		t.Fatalf("Provide for key %v failed: %v", key, err)
	}
}
