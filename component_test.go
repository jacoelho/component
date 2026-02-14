package component_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jacoelho/component"
)

func TestStartupAndShutdownOrder(t *testing.T) {
	ctx := context.Background()
	reg := component.NewRegistry()
	collector := new(eventCollector)

	aKey := component.NewKey[*stubComponent]("A")
	bKey := component.NewKey[*stubComponent]("B")
	cKey := component.NewKey[*stubComponent]("C")

	mustProvide(t, reg, aKey, func(_ *component.Runtime) (*stubComponent, error) {
		return newStub("A", collector), nil
	})
	mustProvide(t, reg, bKey, func(_ *component.Runtime) (*stubComponent, error) {
		return newStub("B", collector), nil
	})
	mustProvide(t, reg, cKey, func(rt *component.Runtime) (*stubComponent, error) {
		a, err := component.Get(rt, aKey)
		if err != nil {
			return nil, err
		}
		b, err := component.Get(rt, bKey)
		if err != nil {
			return nil, err
		}
		_ = a
		_ = b
		return newStub("C", collector), nil
	}, aKey, bKey)

	rt := mustCompileRuntime(t, reg)

	if err := rt.Start(ctx); err != nil {
		t.Fatal("Start:", err)
	}

	if err := rt.Stop(ctx); err != nil {
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
	reg := component.NewRegistry()
	collector := new(eventCollector)

	aKey := component.NewKey[*stubComponent]("A")
	bKey := component.NewKey[*stubComponent]("B")

	mustProvide(t, reg, aKey, func(_ *component.Runtime) (*stubComponent, error) {
		return newStub("A", collector), nil
	}, bKey)

	err := component.Provide(reg, bKey, func(_ *component.Runtime) (*stubComponent, error) {
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
	reg := component.NewRegistry()
	collector := new(eventCollector)

	aKey := component.NewKey[*stubComponent]("A")
	bKey := component.NewKey[*stubComponent]("B")
	cKey := component.NewKey[*stubComponent]("C")

	mustProvide(t, reg, aKey, func(_ *component.Runtime) (*stubComponent, error) {
		return newStub("A", collector), nil
	}, bKey)

	mustProvide(t, reg, bKey, func(_ *component.Runtime) (*stubComponent, error) {
		return newStub("B", collector), nil
	}, cKey)

	err := component.Provide(reg, cKey, func(_ *component.Runtime) (*stubComponent, error) {
		return newStub("C", collector), nil
	}, aKey)
	if err == nil {
		t.Fatal("expected cycle detection when providing C")
	}
	if want := component.ErrCyclicDependency; !errors.Is(err, want) {
		t.Fatalf("got error %v, want %v", err, want)
	}
}

func TestMissingDependencyErrorOnCompile(t *testing.T) {
	reg := component.NewRegistry()
	aKey := component.NewKey[*stubComponent]("A")
	bKey := component.NewKey[*stubComponent]("B")
	collector := new(eventCollector)

	mustProvide(t, reg, aKey, func(_ *component.Runtime) (*stubComponent, error) {
		return newStub("A", collector), nil
	}, bKey)

	_, err := reg.Compile()
	if err == nil {
		t.Fatal("expected compile error with missing dependency")
	}
	if want := component.ErrNotRegistered; !errors.Is(err, want) {
		t.Fatalf("expected error %v, got %v", want, err)
	}

	if events := collector.Events(); len(events) != 0 {
		t.Fatalf("no lifecycle events expected before runtime start, got: %v", events)
	}
}

func TestProvideErrAlreadyRegistered(t *testing.T) {
	reg := component.NewRegistry()
	collector := new(eventCollector)
	aKey := component.NewKey[*stubComponent]("A")

	mustProvide(t, reg, aKey, func(_ *component.Runtime) (*stubComponent, error) {
		return newStub("A1", collector), nil
	})

	err := component.Provide(reg, aKey, func(_ *component.Runtime) (*stubComponent, error) {
		return newStub("A2", collector), nil
	})

	if want := component.ErrAlreadyRegistered; !errors.Is(err, want) {
		t.Fatalf("expected error %v, got %v", want, err)
	}
}

func TestProvideFailureIsAtomic(t *testing.T) {
	reg := component.NewRegistry()
	aKey := component.NewKey[*stubComponent]("A")
	bKey := component.NewKey[*stubComponent]("B")
	collector := new(eventCollector)

	mustProvide(t, reg, aKey, func(_ *component.Runtime) (*stubComponent, error) {
		return newStub("A", collector), nil
	}, bKey)

	err := component.Provide(reg, bKey, func(_ *component.Runtime) (*stubComponent, error) {
		return newStub("B-cycle", collector), nil
	}, aKey)
	if err == nil {
		t.Fatal("expected cycle error")
	}
	if want := component.ErrCyclicDependency; !errors.Is(err, want) {
		t.Fatalf("expected cycle error, got %v", err)
	}

	if err := component.Provide(reg, bKey, func(_ *component.Runtime) (*stubComponent, error) {
		return newStub("B", collector), nil
	}); err != nil {
		t.Fatalf("failed to re-provide B after failed registration: %v", err)
	}

	rt := mustCompileRuntime(t, reg)
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("runtime failed to start after atomic re-provide: %v", err)
	}
}

func TestGet(t *testing.T) {
	reg := component.NewRegistry()
	collector := new(eventCollector)
	ctx := context.Background()

	aKey := component.NewKey[*stubComponent]("A")
	bKey := component.NewKey[*stubComponent]("B")

	mustProvide(t, reg, aKey, func(_ *component.Runtime) (*stubComponent, error) {
		return newStub("A", collector), nil
	})

	rt := mustCompileRuntime(t, reg)

	_, err := component.Get(rt, aKey)
	if want := component.ErrNotStarted; !errors.Is(err, want) {
		t.Fatalf("expected error %v, got %v", want, err)
	}

	if err := rt.Start(ctx); err != nil {
		t.Fatalf("rt.Start failed: %v", err)
	}

	_, err = component.Get(rt, bKey)
	if want := component.ErrNotRegistered; !errors.Is(err, want) {
		t.Fatalf("expected error %v, got %v", want, err)
	}

	compA, err := component.Get(rt, aKey)
	if err != nil {
		t.Fatalf("component.Get(aKey) failed: %v", err)
	}
	if compA.name != "A" {
		t.Errorf("expected component A, got %s", compA.name)
	}

	if err := rt.Stop(ctx); err != nil {
		t.Fatalf("rt.Stop failed: %v", err)
	}

	_, err = component.Get(rt, aKey)
	if want := component.ErrNotStarted; !errors.Is(err, want) {
		t.Fatalf("expected error %v after stop, got %v", want, err)
	}
}

func TestGetDeniedWhenStopping(t *testing.T) {
	reg := component.NewRegistry()
	ctx := context.Background()
	collector := new(eventCollector)

	aKey := component.NewKey[*blockingStopComponent]("A")
	allowStop := make(chan struct{})
	stopEntered := make(chan struct{})

	mustProvide(t, reg, aKey, func(_ *component.Runtime) (*blockingStopComponent, error) {
		return &blockingStopComponent{
			name:        "A",
			collector:   collector,
			stopEntered: stopEntered,
			allowStop:   allowStop,
		}, nil
	})

	rt := mustCompileRuntime(t, reg)
	if err := rt.Start(ctx); err != nil {
		t.Fatalf("start failed: %v", err)
	}

	stopDone := make(chan error, 1)
	go func() {
		stopDone <- rt.Stop(ctx)
	}()

	select {
	case <-stopEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for component to enter stop")
	}

	_, err := component.Get(rt, aKey)
	if want := component.ErrNotStarted; !errors.Is(err, want) {
		t.Fatalf("expected %v while stopping, got %v", want, err)
	}

	close(allowStop)
	if err := <-stopDone; err != nil {
		t.Fatalf("stop failed: %v", err)
	}
}

func TestGetDeniedAfterFailedStop(t *testing.T) {
	reg := component.NewRegistry()
	ctx := context.Background()
	collector := new(eventCollector)
	errSentinel := errors.New("forced stop failure")

	aKey := component.NewKey[*stubComponent]("A")
	stubA := newStub("A", collector)
	stubA.stopErr = errSentinel
	mustProvide(t, reg, aKey, func(_ *component.Runtime) (*stubComponent, error) { return stubA, nil })

	rt := mustCompileRuntime(t, reg)
	if err := rt.Start(ctx); err != nil {
		t.Fatalf("start failed: %v", err)
	}

	if err := rt.Stop(ctx); !errors.Is(err, errSentinel) {
		t.Fatalf("expected stop failure %v, got %v", errSentinel, err)
	}

	_, err := component.Get(rt, aKey)
	if want := component.ErrNotStarted; !errors.Is(err, want) {
		t.Fatalf("expected %v after failed stop, got %v", want, err)
	}
}

func TestStartFailuresAndRollback(t *testing.T) {
	errSentinel := errors.New("forced error")
	aKey := component.NewKey[*stubComponent]("A")
	bKey := component.NewKey[*stubComponent]("B")

	tests := []struct {
		name                         string
		setup                        func(reg *component.Registry, collector *eventCollector)
		expectedError                error
		expectedEventsAfterStartFail []string
	}{
		{
			name: "constructor fails",
			setup: func(reg *component.Registry, collector *eventCollector) {
				mustProvide(t, reg, aKey, func(_ *component.Runtime) (*stubComponent, error) {
					return newStub("A", collector), nil
				})
				mustProvide(t, reg, bKey, func(_ *component.Runtime) (*stubComponent, error) {
					collector.Record("B", "constructor-fail")
					return nil, errSentinel
				}, aKey)
			},
			expectedError:                errSentinel,
			expectedEventsAfterStartFail: []string{"A:start", "B:constructor-fail", "A:stop"},
		},
		{
			name: "constructor panics",
			setup: func(reg *component.Registry, collector *eventCollector) {
				mustProvide(t, reg, aKey, func(_ *component.Runtime) (*stubComponent, error) {
					return newStub("A", collector), nil
				})
				mustProvide(t, reg, bKey, func(_ *component.Runtime) (*stubComponent, error) {
					panic(errSentinel)
				}, aKey)
			},
			expectedError:                component.ErrPanic,
			expectedEventsAfterStartFail: []string{"A:start", "A:stop"},
		},
		{
			name: "start method fails",
			setup: func(reg *component.Registry, collector *eventCollector) {
				stubA := newStub("A", collector)
				stubB := newStub("B", collector)
				stubB.startErr = errSentinel
				mustProvide(t, reg, aKey, func(_ *component.Runtime) (*stubComponent, error) { return stubA, nil })
				mustProvide(t, reg, bKey, func(_ *component.Runtime) (*stubComponent, error) { return stubB, nil }, aKey)
			},
			expectedError:                errSentinel,
			expectedEventsAfterStartFail: []string{"A:start", "B:start-err", "A:stop"},
		},
		{
			name: "start method panics",
			setup: func(reg *component.Registry, collector *eventCollector) {
				stubA := newStub("A", collector)
				stubB := newStub("B", collector)
				stubB.startPanic = true
				mustProvide(t, reg, aKey, func(_ *component.Runtime) (*stubComponent, error) { return stubA, nil })
				mustProvide(t, reg, bKey, func(_ *component.Runtime) (*stubComponent, error) { return stubB, nil }, aKey)
			},
			expectedError:                component.ErrPanic,
			expectedEventsAfterStartFail: []string{"A:start", "A:stop"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reg := component.NewRegistry()
			collector := new(eventCollector)
			ctx := context.Background()

			tc.setup(reg, collector)
			rt := mustCompileRuntime(t, reg)

			if err := rt.Start(ctx); !errors.Is(err, tc.expectedError) {
				t.Errorf("expected error %v, got %v", tc.expectedError, err)
			}

			if events := collector.Events(); !slices.Equal(events, tc.expectedEventsAfterStartFail) {
				t.Errorf("event mismatch after start fail:\ngot:  %v\nwant: %v", events, tc.expectedEventsAfterStartFail)
			}
		})
	}
}

func TestStartRetryAfterSuccessfulRollback(t *testing.T) {
	errSentinel := errors.New("forced start error")
	reg := component.NewRegistry()
	ctx := context.Background()
	collector := new(eventCollector)

	aKey := component.NewKey[*stubComponent]("A")
	bKey := component.NewKey[*stubComponent]("B")

	stubA := newStub("A", collector)
	stubB := newStub("B", collector)
	stubB.startErr = errSentinel

	mustProvide(t, reg, aKey, func(_ *component.Runtime) (*stubComponent, error) { return stubA, nil })
	mustProvide(t, reg, bKey, func(_ *component.Runtime) (*stubComponent, error) { return stubB, nil }, aKey)

	rt := mustCompileRuntime(t, reg)

	err := rt.Start(ctx)
	if !errors.Is(err, errSentinel) {
		t.Fatalf("expected first start error %v, got %v", errSentinel, err)
	}

	if events := collector.Events(); !slices.Equal(events, []string{"A:start", "B:start-err", "A:stop"}) {
		t.Fatalf("unexpected events after failed start rollback: %v", events)
	}

	stubB.startErr = nil
	collector.Clear()

	if err := rt.Start(ctx); err != nil {
		t.Fatalf("expected retry start to succeed after rollback, got %v", err)
	}

	if events := collector.Events(); !slices.Equal(events, []string{"A:start", "B:start"}) {
		t.Fatalf("unexpected events after successful retry start: %v", events)
	}

	if err := rt.Stop(ctx); err != nil {
		t.Fatalf("stop after retry start failed: %v", err)
	}
}

func TestStartFailureWithRollbackFailureRequiresStopRecovery(t *testing.T) {
	startErr := errors.New("forced start error")
	rollbackStopErr := errors.New("forced rollback stop error")
	reg := component.NewRegistry()
	ctx := context.Background()
	collector := new(eventCollector)

	aKey := component.NewKey[*stubComponent]("A")
	bKey := component.NewKey[*stubComponent]("B")

	stubA := newStub("A", collector)
	stubA.stopErr = rollbackStopErr
	stubB := newStub("B", collector)
	stubB.startErr = startErr

	mustProvide(t, reg, aKey, func(_ *component.Runtime) (*stubComponent, error) { return stubA, nil })
	mustProvide(t, reg, bKey, func(_ *component.Runtime) (*stubComponent, error) { return stubB, nil }, aKey)

	rt := mustCompileRuntime(t, reg)

	err := rt.Start(ctx)
	if !errors.Is(err, startErr) || !errors.Is(err, rollbackStopErr) {
		t.Fatalf("expected joined start/rollback errors, got %v", err)
	}

	stubB.startErr = nil
	if err := rt.Start(ctx); !errors.Is(err, component.ErrInvalidStateTransition) {
		t.Fatalf("expected invalid transition retrying start after failed rollback, got %v", err)
	}

	stubA.stopErr = nil
	if err := rt.Stop(ctx); err != nil {
		t.Fatalf("stop should recover runtime from failed_start, got %v", err)
	}
}

func TestStopRejectedWhileStartRollbackInProgress(t *testing.T) {
	startErr := errors.New("forced start error")
	reg := component.NewRegistry()
	ctx := context.Background()
	collector := new(eventCollector)

	aKey := component.NewKey[*blockingStopComponent]("A")
	bKey := component.NewKey[*stubComponent]("B")

	stopEntered := make(chan struct{})
	allowStop := make(chan struct{})

	stubA := &blockingStopComponent{
		name:        "A",
		collector:   collector,
		stopEntered: stopEntered,
		allowStop:   allowStop,
	}
	stubB := newStub("B", collector)
	stubB.startErr = startErr

	mustProvide(t, reg, aKey, func(_ *component.Runtime) (*blockingStopComponent, error) { return stubA, nil })
	mustProvide(t, reg, bKey, func(_ *component.Runtime) (*stubComponent, error) { return stubB, nil }, aKey)

	rt := mustCompileRuntime(t, reg)

	startDone := make(chan error, 1)
	go func() {
		startDone <- rt.Start(ctx)
	}()

	select {
	case <-stopEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for rollback to begin")
	}

	stopErr := rt.Stop(ctx)
	if !errors.Is(stopErr, component.ErrInvalidStateTransition) {
		t.Fatalf("expected stop to be rejected during rollback, got %v", stopErr)
	}

	close(allowStop)

	err := <-startDone
	if !errors.Is(err, startErr) {
		t.Fatalf("expected start error %v, got %v", startErr, err)
	}
	if errors.Is(err, component.ErrInvalidStateTransition) {
		t.Fatalf("start returned unexpected invalid state transition error: %v", err)
	}

	if events := collector.Events(); !slices.Equal(events, []string{"A:start", "B:start-err", "A:stop"}) {
		t.Fatalf("unexpected events during rollback race test: %v", events)
	}

	stubB.startErr = nil
	collector.Clear()
	if err := rt.Start(ctx); err != nil {
		t.Fatalf("expected start retry to succeed after rollback, got %v", err)
	}
	if err := rt.Stop(ctx); err != nil {
		t.Fatalf("stop after retry start failed: %v", err)
	}
}

func TestStopFailuresAndContinuation(t *testing.T) {
	errSentinel := errors.New("forced error")
	aKey := component.NewKey[*stubComponent]("A")
	bKey := component.NewKey[*stubComponent]("B")
	cKey := component.NewKey[*stubComponent]("C")

	tests := []struct {
		name           string
		setup          func(reg *component.Registry, collector *eventCollector)
		expectedError  error
		expectedEvents [][]string
	}{
		{
			name: "no components provided",
			setup: func(reg *component.Registry, collector *eventCollector) {
			},
			expectedError:  nil,
			expectedEvents: [][]string{},
		},
		{
			name: "one component fails to stop (level 0)",
			setup: func(reg *component.Registry, collector *eventCollector) {
				stubA := newStub("A", collector)
				stubA.stopErr = errSentinel
				stubB := newStub("B", collector)
				mustProvide(t, reg, aKey, func(_ *component.Runtime) (*stubComponent, error) { return stubA, nil })
				mustProvide(t, reg, bKey, func(_ *component.Runtime) (*stubComponent, error) { return stubB, nil })
			},
			expectedError: errSentinel,
			expectedEvents: [][]string{
				{"B:stop", "A:stop-err"},
			},
		},
		{
			name: "multiple components fail to stop at same level",
			setup: func(reg *component.Registry, collector *eventCollector) {
				stubA := newStub("A", collector)
				stubA.stopErr = errSentinel
				stubB := newStub("B", collector)
				stubB.stopErr = errSentinel
				mustProvide(t, reg, aKey, func(_ *component.Runtime) (*stubComponent, error) { return stubA, nil })
				mustProvide(t, reg, bKey, func(_ *component.Runtime) (*stubComponent, error) { return stubB, nil })
			},
			expectedError: errSentinel,
			expectedEvents: [][]string{
				{"B:stop-err", "A:stop-err"},
			},
		},
		{
			name: "higher-level component fails to stop, lower level still stops",
			setup: func(reg *component.Registry, collector *eventCollector) {
				stubA := newStub("A", collector)
				stubB := newStub("B", collector)
				stubB.stopErr = errSentinel
				mustProvide(t, reg, aKey, func(_ *component.Runtime) (*stubComponent, error) { return stubA, nil })
				mustProvide(t, reg, bKey, func(_ *component.Runtime) (*stubComponent, error) { return stubB, nil }, aKey)
			},
			expectedError: errSentinel,
			expectedEvents: [][]string{
				{"B:stop-err"},
				{"A:stop"},
			},
		},
		{
			name: "higher-level component panics on stop, lower level still stops",
			setup: func(reg *component.Registry, collector *eventCollector) {
				stubA := newStub("A", collector)
				stubB := newStub("B", collector)
				stubB.stopPanic = true
				mustProvide(t, reg, aKey, func(_ *component.Runtime) (*stubComponent, error) { return stubA, nil })
				mustProvide(t, reg, bKey, func(_ *component.Runtime) (*stubComponent, error) { return stubB, nil }, aKey)
			},
			expectedError: component.ErrPanic,
			expectedEvents: [][]string{
				{"A:stop"},
			},
		},
		{
			name: "multiple components fail to stop at different levels",
			setup: func(reg *component.Registry, collector *eventCollector) {
				stubA := newStub("A", collector)
				stubA.stopErr = errSentinel
				stubB := newStub("B", collector)
				stubC := newStub("C", collector)
				stubC.stopErr = errSentinel
				mustProvide(t, reg, aKey, func(_ *component.Runtime) (*stubComponent, error) { return stubA, nil })
				mustProvide(t, reg, bKey, func(_ *component.Runtime) (*stubComponent, error) { return stubB, nil })
				mustProvide(t, reg, cKey, func(_ *component.Runtime) (*stubComponent, error) { return stubC, nil }, aKey)
			},
			expectedError: errSentinel,
			expectedEvents: [][]string{
				{"C:stop-err"},
				{"B:stop", "A:stop-err"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reg := component.NewRegistry()
			collector := new(eventCollector)
			ctx := context.Background()

			tc.setup(reg, collector)
			rt := mustCompileRuntime(t, reg)

			if err := rt.Start(ctx); err != nil {
				t.Fatalf("unexpected error starting runtime: %v", err)
			}

			collector.Clear()

			if err := rt.Stop(ctx); !errors.Is(err, tc.expectedError) {
				t.Errorf("expected error %v, got %v", tc.expectedError, err)
			}

			assertEventGroupsMatch(t, tc.expectedEvents, collector.Events())
		})
	}
}

func TestStopRetryAfterTransientFailure(t *testing.T) {
	errSentinel := errors.New("transient stop error")
	reg := component.NewRegistry()
	ctx := context.Background()
	collector := new(eventCollector)

	aKey := component.NewKey[*stubComponent]("A")
	stubA := newStub("A", collector)
	stubA.stopErr = errSentinel

	mustProvide(t, reg, aKey, func(_ *component.Runtime) (*stubComponent, error) { return stubA, nil })

	rt := mustCompileRuntime(t, reg)

	if err := rt.Start(ctx); err != nil {
		t.Fatalf("start failed: %v", err)
	}

	collector.Clear()

	if err := rt.Stop(ctx); !errors.Is(err, errSentinel) {
		t.Fatalf("expected stop failure %v, got %v", errSentinel, err)
	}

	stubA.stopErr = nil
	if err := rt.Stop(ctx); err != nil {
		t.Fatalf("expected stop retry to succeed from failed_stop state, got %v", err)
	}

	if events := collector.Events(); !slices.Equal(events, []string{"A:stop-err", "A:stop"}) {
		t.Fatalf("unexpected stop retry events: %v", events)
	}
}

func TestRuntimeStateTransitions(t *testing.T) {
	reg := component.NewRegistry()
	aKey := component.NewKey[*stubComponent]("A")
	collector := new(eventCollector)

	mustProvide(t, reg, aKey, func(_ *component.Runtime) (*stubComponent, error) {
		return newStub("A", collector), nil
	})

	rt := mustCompileRuntime(t, reg)
	ctx := context.Background()

	if err := rt.Stop(ctx); !errors.Is(err, component.ErrInvalidStateTransition) {
		t.Fatalf("expected invalid transition stopping before start, got: %v", err)
	}

	if err := rt.Start(ctx); err != nil {
		t.Fatalf("start failed: %v", err)
	}

	if err := rt.Start(ctx); !errors.Is(err, component.ErrAlreadyStarted) {
		t.Fatalf("expected already started error, got: %v", err)
	}

	if err := rt.Stop(ctx); err != nil {
		t.Fatalf("stop failed: %v", err)
	}

	if err := rt.Stop(ctx); !errors.Is(err, component.ErrInvalidStateTransition) {
		t.Fatalf("expected invalid transition stopping twice, got: %v", err)
	}

	if err := rt.Start(ctx); err != nil {
		t.Fatalf("restart from stopped failed: %v", err)
	}

	if err := rt.Stop(ctx); err != nil {
		t.Fatalf("stop after restart failed: %v", err)
	}
}

func TestConcurrentStartRejectedDuringStart(t *testing.T) {
	reg := component.NewRegistry()
	blockKey := component.NewKey[*blockingComponent]("block")

	started := make(chan struct{})
	release := make(chan struct{})

	mustProvide(t, reg, blockKey, func(_ *component.Runtime) (*blockingComponent, error) {
		return &blockingComponent{started: started, release: release}, nil
	})

	rt := mustCompileRuntime(t, reg)

	startDone := make(chan error, 1)
	go func() {
		startDone <- rt.Start(context.Background())
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first start to block")
	}

	err := rt.Start(context.Background())
	if !errors.Is(err, component.ErrInvalidStateTransition) {
		t.Fatalf("expected invalid transition while start in progress, got: %v", err)
	}

	close(release)
	if err := <-startDone; err != nil {
		t.Fatalf("first start returned error: %v", err)
	}

	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("stop failed: %v", err)
	}
}

func TestDotGraphDeterministic(t *testing.T) {
	reg := component.NewRegistry()
	aKey := component.NewKey[*stubComponent]("A")
	bKey := component.NewKey[*stubComponent]("B")
	cKey := component.NewKey[*stubComponent]("C")
	collector := new(eventCollector)

	mustProvide(t, reg, aKey, func(_ *component.Runtime) (*stubComponent, error) {
		return newStub("A", collector), nil
	})
	mustProvide(t, reg, bKey, func(_ *component.Runtime) (*stubComponent, error) {
		return newStub("B", collector), nil
	})
	mustProvide(t, reg, cKey, func(_ *component.Runtime) (*stubComponent, error) {
		return newStub("C", collector), nil
	}, aKey, bKey)

	plan, err := reg.Compile()
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	dot1 := plan.DotGraph()
	dot2 := plan.DotGraph()
	if dot1 != dot2 {
		t.Fatalf("dot graph should be deterministic")
	}

	if len(dot1) == 0 {
		t.Fatalf("dot graph should not be empty")
	}
}

func TestNilPlanNewRuntime(t *testing.T) {
	var plan *component.Plan
	rt, err := plan.NewRuntime()
	if !errors.Is(err, component.ErrNilPlan) {
		t.Fatalf("expected %v, got %v", component.ErrNilPlan, err)
	}
	if rt != nil {
		t.Fatalf("expected nil runtime for nil plan")
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
		panic(fmt.Sprintf("component %q stop panic", s.name))
	}
	if s.stopErr != nil {
		s.collector.Record(s.name, "stop-err")
		return s.stopErr
	}
	s.collector.Record(s.name, "stop")
	return nil
}

type blockingComponent struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingComponent) Start(_ context.Context) error {
	b.once.Do(func() { close(b.started) })
	<-b.release
	return nil
}

func (b *blockingComponent) Stop(_ context.Context) error {
	return nil
}

type blockingStopComponent struct {
	name        string
	collector   *eventCollector
	stopEntered chan struct{}
	allowStop   chan struct{}
	once        sync.Once
}

func (b *blockingStopComponent) Start(_ context.Context) error {
	b.collector.Record(b.name, "start")
	return nil
}

func (b *blockingStopComponent) Stop(_ context.Context) error {
	b.once.Do(func() { close(b.stopEntered) })
	<-b.allowStop
	b.collector.Record(b.name, "stop")
	return nil
}

func mustProvide[T component.Lifecycle](
	t *testing.T,
	reg *component.Registry,
	key component.Key[T],
	fn component.Constructor[T],
	deps ...component.Keyer,
) {
	t.Helper()
	if err := component.Provide(reg, key, fn, deps...); err != nil {
		t.Fatalf("Provide for key %v failed: %v", key, err)
	}
}

func mustCompileRuntime(t *testing.T, reg *component.Registry) *component.Runtime {
	t.Helper()
	plan, err := reg.Compile()
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}
	rt, err := plan.NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime failed: %v", err)
	}
	return rt
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
