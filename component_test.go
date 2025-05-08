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

	if err := sys.Stop(ctx); err != nil {
		t.Fatal("Stop:", err)
	}

	expectedEvents := [][]string{
		{"A:start", "B:start"},
		{"C:start"},
		{"C:stop"},
		{"A:stop", "B:stop"},
	}

	assertEventGroupsMatch(t, expectedEvents, collector.Events())
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
	if want := component.ErrNotStarted; !errors.Is(err, want) {
		t.Fatalf("expected error %v, got %v", want, err)
	}

	// Start the system
	if err := sys.Start(ctx); err != nil {
		t.Fatalf("sys.Start failed: %v", err)
	}

	// 2. Get unregistered component (after system start)
	_, err = component.Get(sys, bKey)
	if want := component.ErrNotRegistered; !errors.Is(err, want) {
		t.Fatalf("expected error %v, got %v", want, err)
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

func TestStartFailuresAndRollback(t *testing.T) {
	errSentinel := errors.New("forced error")
	aKey := component.NewKey[*stubComponent]("A")
	bKey := component.NewKey[*stubComponent]("B")

	tests := []struct {
		name                         string
		setup                        func(sys *component.System, collector *eventCollector)
		expectedError                error
		expectedEventsAfterStartFail []string // To check rollback
	}{
		{
			name: "constructor fails",
			setup: func(sys *component.System, collector *eventCollector) {
				mustProvide(t, sys, aKey, func(_ *component.System) (*stubComponent, error) {
					return newStub("A", collector), nil
				})
				// B's constructor fails
				mustProvide(t, sys, bKey, func(_ *component.System) (*stubComponent, error) {
					collector.Record("B", "constructor-fail")
					return nil, errSentinel
				}, aKey)
			},
			expectedError:                errSentinel,
			expectedEventsAfterStartFail: []string{"A:start", "B:constructor-fail", "A:stop"},
		},
		{
			name: "constructor panics",
			setup: func(sys *component.System, collector *eventCollector) {
				mustProvide(t, sys, aKey, func(_ *component.System) (*stubComponent, error) {
					return newStub("A", collector), nil
				})
				// B's constructor fails
				mustProvide(t, sys, bKey, func(_ *component.System) (*stubComponent, error) {
					panic(errSentinel)
					return newStub("B", collector), nil
				}, aKey)
			},
			expectedError:                component.ErrPanic,
			expectedEventsAfterStartFail: []string{"A:start", "A:stop"},
		},
		{
			name: "start method fails",
			setup: func(sys *component.System, collector *eventCollector) {
				stubA := newStub("A", collector)
				stubB := newStub("B", collector)
				stubB.startErr = errSentinel // B's Start method will fail

				mustProvide(t, sys, aKey, func(_ *component.System) (*stubComponent, error) { return stubA, nil })
				mustProvide(t, sys, bKey, func(_ *component.System) (*stubComponent, error) { return stubB, nil }, aKey)
			},
			expectedError:                errSentinel,
			expectedEventsAfterStartFail: []string{"A:start", "B:start-err", "A:stop"},
		},
		{
			name: "start method panics",
			setup: func(sys *component.System, collector *eventCollector) {
				stubA := newStub("A", collector)
				stubB := newStub("B", collector)
				stubB.startPanic = true // B's Start method will fail

				mustProvide(t, sys, aKey, func(_ *component.System) (*stubComponent, error) { return stubA, nil })
				mustProvide(t, sys, bKey, func(_ *component.System) (*stubComponent, error) { return stubB, nil }, aKey)
			},
			expectedError:                component.ErrPanic,
			expectedEventsAfterStartFail: []string{"A:start", "A:stop"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sys := new(component.System)
			collector := new(eventCollector)
			ctx := context.Background()

			tc.setup(sys, collector)

			if err := sys.Start(ctx); !errors.Is(err, tc.expectedError) {
				t.Errorf("expected error %v, got %v", tc.expectedError, err)
			}

			if events := collector.Events(); !slices.Equal(events, tc.expectedEventsAfterStartFail) {
				t.Errorf("event mismatch after start fail:\ngot:  %v\nwant: %v", events, tc.expectedEventsAfterStartFail)
			}
		})
	}
}

func TestStopFailuresAndContinuation(t *testing.T) {
	errSentinel := errors.New("forced error")
	aKey := component.NewKey[*stubComponent]("A")
	bKey := component.NewKey[*stubComponent]("B")
	cKey := component.NewKey[*stubComponent]("C")

	tests := []struct {
		name           string
		setup          func(sys *component.System, collector *eventCollector)
		expectedError  error
		expectedEvents [][]string
	}{
		{
			name: "no components provided, stop does nothing",
			setup: func(sys *component.System, collector *eventCollector) {
				// No components provided
			},
			expectedError:  nil, // No error
			expectedEvents: [][]string{},
		},
		{
			name: "one component fails to stop (level 0)",
			setup: func(sys *component.System, collector *eventCollector) {
				stubA := newStub("A", collector)
				stubA.stopErr = errSentinel // A's Stop method will fail
				stubB := newStub("B", collector)

				mustProvide(t, sys, aKey, func(_ *component.System) (*stubComponent, error) { return stubA, nil })
				mustProvide(t, sys, bKey, func(_ *component.System) (*stubComponent, error) { return stubB, nil })
			},
			expectedError: errSentinel,
			expectedEvents: [][]string{
				{
					"B:stop",     // B stops successfully (parallel to A's attempt)
					"A:stop-err", // A attempts stop and errors
				},
			},
		},
		{
			name: "multiple components fail to stop at same level",
			setup: func(sys *component.System, collector *eventCollector) {
				stubA := newStub("A", collector)
				stubA.stopErr = errSentinel
				stubB := newStub("B", collector)
				stubB.stopErr = errSentinel

				mustProvide(t, sys, aKey, func(_ *component.System) (*stubComponent, error) { return stubA, nil })
				mustProvide(t, sys, bKey, func(_ *component.System) (*stubComponent, error) { return stubB, nil })
			},
			expectedError: errSentinel,
			expectedEvents: [][]string{
				{"B:stop-err", "A:stop-err"},
			},
		},
		{
			name: "higher-level component fails to stop, lower level still stops",
			setup: func(sys *component.System, collector *eventCollector) {
				stubA := newStub("A", collector)
				stubB := newStub("B", collector)
				stubB.stopErr = errSentinel

				mustProvide(t, sys, aKey, func(_ *component.System) (*stubComponent, error) { return stubA, nil })
				mustProvide(t, sys, bKey, func(_ *component.System) (*stubComponent, error) { return stubB, nil }, aKey)
			},
			expectedError: errSentinel,
			expectedEvents: [][]string{
				{"B:stop-err"}, // B (level 1) stops first and errors
				{"A:stop"},     // A (level 0) stops after B
			},
		},
		{
			name: "higher-level component panics on stop, lower level still stops",
			setup: func(sys *component.System, collector *eventCollector) {
				stubA := newStub("A", collector)
				stubB := newStub("B", collector)
				stubB.stopPanic = true

				mustProvide(t, sys, aKey, func(_ *component.System) (*stubComponent, error) { return stubA, nil })
				mustProvide(t, sys, bKey, func(_ *component.System) (*stubComponent, error) { return stubB, nil }, aKey)
			},
			expectedError: component.ErrPanic,
			expectedEvents: [][]string{
				// B (level 1) panics before recording "stop"
				{"A:stop"}, // A (level 0) stops after B's panic is handled
			},
		},
		{
			name: "multiple components fail to stop at different levels",
			setup: func(sys *component.System, collector *eventCollector) {
				stubA := newStub("A", collector)
				stubA.stopErr = errSentinel // Level 0, depends on nothing

				stubB := newStub("B", collector) // Level 0, depends on nothing

				stubC := newStub("C", collector) // Level 1
				stubC.stopErr = errSentinel

				mustProvide(t, sys, aKey, func(_ *component.System) (*stubComponent, error) { return stubA, nil })
				mustProvide(t, sys, bKey, func(_ *component.System) (*stubComponent, error) { return stubB, nil })
				mustProvide(t, sys, cKey, func(_ *component.System) (*stubComponent, error) { return stubC, nil }, aKey)
			},
			expectedError: errSentinel,
			expectedEvents: [][]string{
				{"C:stop-err"}, // C (L1) fails
				{
					"B:stop",     // B (L0) succeeds
					"A:stop-err", // A (L0) fails
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sys := new(component.System)
			collector := new(eventCollector)
			ctx := context.Background()

			tc.setup(sys, collector)

			if err := sys.Start(ctx); err != nil {
				t.Fatalf("unexpected error starting component: %v", err)
			}

			collector.Clear()

			if err := sys.Stop(ctx); !errors.Is(err, tc.expectedError) {
				t.Errorf("expected error %v, got %v", tc.expectedError, err)
			}

			assertEventGroupsMatch(t, tc.expectedEvents, collector.Events())
		})
	}
}

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
	name       string
	collector  *eventCollector
	startErr   error
	startPanic bool
	stopErr    error
	stopPanic  bool
}

func newStub(name string, collector *eventCollector) *stubComponent {
	return &stubComponent{
		name:      name,
		collector: collector,
	}
}

func (s *stubComponent) Start(_ context.Context) error {
	if s.startPanic {
		panic(fmt.Sprintf("component %q start panic", s.name))
	}
	if s.startErr != nil {
		s.collector.Record(s.name, "start-err")
		return s.startErr
	}
	s.collector.Record(s.name, "start")
	return nil
}

func (s *stubComponent) Stop(_ context.Context) error {
	if s.stopPanic {
		panic(fmt.Sprintf("component %q start panic", s.name))
	}
	if s.stopErr != nil {
		s.collector.Record(s.name, "stop-err")
		return s.stopErr
	}
	s.collector.Record(s.name, "stop")
	return nil
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

// assertEventGroupsMatch checks if the actual events match the expected event groups.
// expectedEventGroups defines sequences, where an inner slice represents a group of events.
// group element order is not significant and events are compared after sorting.
func assertEventGroupsMatch(
	t *testing.T,
	expectedEventGroups [][]string,
	actualEvents []string,
) {
	t.Helper()

	totalExpectedEventCount := 0
	for _, group := range expectedEventGroups {
		totalExpectedEventCount += len(group)
	}

	if len(actualEvents) != totalExpectedEventCount {
		t.Errorf("total event count mismatch: got %d events (%v), want %d events from groups (%v)",
			len(actualEvents), actualEvents, totalExpectedEventCount, expectedEventGroups)
		return
	}

	if totalExpectedEventCount == 0 {
		return
	}

	index := 0
	for i, expectedGroup := range expectedEventGroups {
		groupName := fmt.Sprintf("event group %d (expected %v)", i, expectedGroup)
		actualGroupSegment := actualEvents[index : index+len(expectedGroup)]
		assertEventsSortedMatch(t, actualGroupSegment, expectedGroup, groupName)
		index += len(expectedGroup)
	}
}

func assertEventsSortedMatch(t *testing.T, actual, expected []string, contextMsg string) {
	t.Helper()

	actualCopy := slices.Clone(actual)
	expectedCopy := slices.Clone(expected)

	slices.Sort(actualCopy)
	slices.Sort(expectedCopy)

	if !slices.Equal(actualCopy, expectedCopy) {
		t.Errorf("%s: event content mismatch (after sorting):\ngot_sorted:  %v (from original %v)\nwant_sorted: %v (from original %v)",
			contextMsg, actualCopy, actual, expectedCopy, expected)
	}
}
