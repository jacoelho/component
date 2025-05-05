package component_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/jacoelho/component"
)

// stubComponent implements Lifecycle and records events.
type stubComponent struct {
	name   string
	events *[]string
	mu     *sync.Mutex
}

func (s *stubComponent) Start(ctx context.Context) error {
	s.mu.Lock()
	*s.events = append(*s.events, s.name+":start")
	s.mu.Unlock()
	return nil
}

func (s *stubComponent) Stop(ctx context.Context) error {
	s.mu.Lock()
	*s.events = append(*s.events, s.name+":stop")
	s.mu.Unlock()
	return nil
}

func TestStartupAndShutdownOrder(t *testing.T) {
	var events []string
	mu := &sync.Mutex{}
	ctx := context.Background()
	sys := new(component.System)

	// Declare keys
	aKey := component.NewKey[*stubComponent]("A")
	bKey := component.NewKey[*stubComponent]("B")
	cKey := component.NewKey[*stubComponent]("C")

	// Provide components: A and B are level 0, C depends on A and B
	if err := component.Provide(sys, aKey, func(_ *component.System) (*stubComponent, error) {
		return &stubComponent{"A", &events, mu}, nil
	}); err != nil {
		t.Fatal("Provide A:", err)
	}
	if err := component.Provide(sys, bKey, func(_ *component.System) (*stubComponent, error) {
		return &stubComponent{"B", &events, mu}, nil
	}); err != nil {
		t.Fatal("Provide B:", err)
	}
	if err := component.Provide(sys, cKey, func(s *component.System) (*stubComponent, error) {
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
		return &stubComponent{"C", &events, mu}, nil
	}, aKey, bKey); err != nil {
		t.Fatal("Provide C:", err)
	}

	// Start system
	if err := sys.Start(ctx); err != nil {
		t.Fatal("Start:", err)
	}

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

	// Stop system
	if err := sys.Stop(ctx); err != nil {
		t.Fatal("Stop:", err)
	}

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
	sys := &component.System{}
	aKey := component.NewKey[*stubComponent]("A")
	bKey := component.NewKey[*stubComponent]("B")

	// A depends on B
	if err := component.Provide(sys, aKey, func(_ *component.System) (*stubComponent, error) {
		return &stubComponent{"A", nil, nil}, nil
	}, bKey); err != nil {
		t.Fatal("Provide A:", err)
	}

	// B depends on A -> cycle
	if err := component.Provide(sys, bKey, func(_ *component.System) (*stubComponent, error) {
		return &stubComponent{"B", nil, nil}, nil
	}, aKey); err == nil {
		t.Fatal("expected cycle detection when providing B")
	}
}

func TestCycleDetectionIndirect(t *testing.T) {
	sys := &component.System{}
	aKey := component.NewKey[*stubComponent]("A")
	bKey := component.NewKey[*stubComponent]("B")
	cKey := component.NewKey[*stubComponent]("C")

	// A depends on B
	if err := component.Provide(sys, aKey, func(_ *component.System) (*stubComponent, error) {
		return &stubComponent{"A", nil, nil}, nil
	}, bKey); err != nil {
		t.Fatal("Provide A:", err)
	}

	// B depends on C
	if err := component.Provide(sys, bKey, func(_ *component.System) (*stubComponent, error) {
		return &stubComponent{"B", nil, nil}, nil
	}, cKey); err != nil {
		t.Fatal("Provide B:", err)
	}

	// C depends on A --> cycle
	if err := component.Provide(sys, cKey, func(_ *component.System) (*stubComponent, error) {
		return &stubComponent{"C", nil, nil}, nil
	}, aKey); err == nil {
		t.Fatal("expected cycle detection when providing C")
	}
}

func TestMissingDependencyErrorOnStart(t *testing.T) {
	sys := &component.System{}
	aKey := component.NewKey[*stubComponent]("A")
	bKey := component.NewKey[*stubComponent]("B")

	// A depends on B, but B not provided
	if err := component.Provide(sys, aKey, func(_ *component.System) (*stubComponent, error) {
		return &stubComponent{"A", nil, nil}, nil
	}, bKey); err != nil {
		t.Fatal("Provide A:", err)
	}

	// Start should error about missing dependency B
	err := sys.Start(context.Background())
	if err == nil {
		t.Fatal("expected error on Start with missing dependency")
	}
	if want := component.ErrNotRegistered; !errors.Is(err, want) {
		t.Fatalf("got error %v, want %v", err, want)
	}
}
