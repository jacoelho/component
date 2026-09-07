package component_test

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	component "github.com/jacoelho/component"
)

type runtimeResource struct {
	name    string
	startFn func(context.Context) error
	stopFn  func(context.Context) error
}

func (r *runtimeResource) Start(ctx context.Context) error {
	if r.startFn == nil {
		return nil
	}
	return r.startFn(ctx)
}

func (r *runtimeResource) Stop(ctx context.Context) error {
	if r.stopFn == nil {
		return nil
	}
	return r.stopFn(ctx)
}

type exactContext struct {
	context.Context
}

func waitSignal(t *testing.T, signal <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal(message)
	}
}

func waitResult(t *testing.T, results <-chan error, message string) error {
	t.Helper()
	select {
	case err := <-results:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal(message)
		return nil
	}
}

func makeRelease(t *testing.T) (chan struct{}, func()) {
	t.Helper()
	release := make(chan struct{})
	var once sync.Once
	closeRelease := func() { once.Do(func() { close(release) }) }
	t.Cleanup(closeRelease)
	return release, closeRelease
}

func nodeError(t *testing.T, err error) *component.NodeError {
	t.Helper()
	var result *component.NodeError
	if !errors.As(err, &result) {
		t.Fatalf("error did not expose a NodeError")
	}
	return result
}

func TestManagedChainStartsInDependencyOrderAndStopsInReverse(t *testing.T) {
	var mu sync.Mutex
	var events []string
	record := func(event string) {
		mu.Lock()
		events = append(events, event)
		mu.Unlock()
	}
	dependency := component.ProvideValue(func() *runtimeResource {
		record("dependency.construct")
		return &runtimeResource{
			name: "dependency",
			startFn: func(context.Context) error {
				record("dependency.start")
				return nil
			},
			stopFn: func(context.Context) error {
				record("dependency.stop")
				return nil
			},
		}
	}, component.Managed[*runtimeResource]())
	dependent := component.MapValue(dependency, func(dep *runtimeResource) *runtimeResource {
		record("dependent.construct")
		return &runtimeResource{
			name: dep.name + ".child",
			startFn: func(context.Context) error {
				record("dependent.start")
				return nil
			},
			stopFn: func(context.Context) error {
				record("dependent.stop")
				return nil
			},
		}
	}, component.Managed[*runtimeResource]())

	rt := newTestRuntime(t, dependent)
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("Start returned an error")
	}
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("Stop returned an error")
	}
	mu.Lock()
	got := append([]string(nil), events...)
	mu.Unlock()
	want := []string{
		"dependency.construct",
		"dependency.start",
		"dependent.construct",
		"dependent.start",
		"dependent.stop",
		"dependency.stop",
	}
	if len(got) != len(want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("events = %v, want %v", got, want)
		}
	}
}

func TestIndependentNodesStartSeriallyAndStopInReverse(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered := make(chan string, 6)
		release := make(chan struct{})
		t.Cleanup(func() { close(release) })
		dispatch := func(name string) {
			entered <- name
			<-release
		}
		resource := func(name string) component.Ref[*runtimeResource] {
			return component.ProvideValue(func() *runtimeResource {
				dispatch(name + ".construct")
				return &runtimeResource{
					startFn: func(context.Context) error {
						dispatch(name + ".start")
						return nil
					},
					stopFn: func(context.Context) error {
						dispatch(name + ".stop")
						return nil
					},
				}
			}, component.Managed[*runtimeResource]())
		}
		rt := newTestRuntime(t, resource("a"), resource("b"))
		next := func() string {
			synctest.Wait()
			if got := len(entered); got != 1 {
				t.Fatalf("callbacks entered concurrently: queued=%d", got)
			}
			name := <-entered
			release <- struct{}{}
			return name
		}
		startResult := make(chan error, 1)
		go func() { startResult <- rt.Start(context.Background()) }()
		constructA := next()
		startA := next()
		constructB := next()
		startB := next()
		synctest.Wait()
		if err := <-startResult; err != nil {
			t.Fatalf("Start returned an error: %v", err)
		}

		phaseName := func(event, wantPhase string) string {
			name, phase, ok := strings.Cut(event, ".")
			if !ok || phase != wantPhase {
				t.Fatalf("event=%q, want phase %q", event, wantPhase)
			}
			return name
		}
		constructNameA := phaseName(constructA, "construct")
		startNameA := phaseName(startA, "start")
		constructNameB := phaseName(constructB, "construct")
		startNameB := phaseName(startB, "start")
		if constructNameA != startNameA || constructNameB != startNameB {
			t.Fatalf("construct/start names = %q/%q and %q/%q", constructNameA, startNameA, constructNameB, startNameB)
		}
		if constructNameA == constructNameB {
			t.Fatalf("only one independent node dispatched: %q", constructNameA)
		}

		stopResult := make(chan error, 1)
		go func() { stopResult <- rt.Stop(context.Background()) }()
		stopA := phaseName(next(), "stop")
		stopB := phaseName(next(), "stop")
		synctest.Wait()
		if err := <-stopResult; err != nil {
			t.Fatalf("Stop returned an error: %v", err)
		}
		if stopA != startNameB || stopB != startNameA {
			t.Fatalf("stop order=%q,%q; startup order=%q,%q", stopA, stopB, startNameA, startNameB)
		}
	})
}

func TestFailedStartRetainsSuccessfulOwnershipForCallerStop(t *testing.T) {
	startFailure := errors.New("sibling construction failed")
	var sourceStops, laterCalls atomic.Int32
	source := component.ProvideValue(func() *runtimeResource {
		return &runtimeResource{name: "source", stopFn: func(context.Context) error {
			sourceStops.Add(1)
			return nil
		}}
	}, component.Managed[*runtimeResource]())
	failing := component.Provide(func() (*runtimeResource, error) {
		return nil, startFailure
	})
	later := component.ProvideValue(func() int {
		laterCalls.Add(1)
		return 1
	})
	rt := newTestRuntime(t, source, failing, later)
	if err := rt.Start(context.Background()); err == nil || !errors.Is(err, startFailure) {
		t.Fatalf("Start did not return the constructor failure")
	}
	if got := sourceStops.Load(); got != 0 {
		t.Fatalf("Start performed implicit cleanup: stop calls=%d", got)
	}
	if got := laterCalls.Load(); got != 0 {
		t.Fatalf("Start continued after failure: later factory calls=%d", got)
	}
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("caller Stop returned an error")
	}
	if got := sourceStops.Load(); got != 1 {
		t.Fatalf("source stop calls=%d, want 1", got)
	}
}

func TestStartHookFailureTransfersOwnershipBeforeCallingTheHook(t *testing.T) {
	startFailure := errors.New("start hook failed")
	var starts, stops atomic.Int32
	ref := component.ProvideValue(func() *runtimeResource {
		return &runtimeResource{
			name: "owned",
			startFn: func(context.Context) error {
				starts.Add(1)
				return startFailure
			},
			stopFn: func(context.Context) error {
				stops.Add(1)
				return nil
			},
		}
	}, component.Managed[*runtimeResource]())
	rt := newTestRuntime(t, ref)
	if err := rt.Start(context.Background()); err == nil || !errors.Is(err, startFailure) {
		t.Fatalf("Start did not return the hook failure")
	}
	if starts.Load() != 1 || stops.Load() != 0 {
		t.Fatalf("unexpected hook counts before Stop: starts=%d stops=%d", starts.Load(), stops.Load())
	}
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("Stop after failed Start returned an error: %v", err)
	}
	if got := stops.Load(); got != 1 {
		t.Fatalf("Stop after failed Start called cleanup %d times, want 1", got)
	}
}

func TestConstructorValueAndErrorDoesNotTransferOwnership(t *testing.T) {
	constructionFailure := errors.New("constructor failed after a value")
	var starts, stops atomic.Int32
	ref := component.Provide(func() (*runtimeResource, error) {
		return &runtimeResource{
			name: "discarded",
			startFn: func(context.Context) error {
				starts.Add(1)
				return nil
			},
			stopFn: func(context.Context) error {
				stops.Add(1)
				return nil
			},
		}, constructionFailure
	}, component.Managed[*runtimeResource]())
	rt := newTestRuntime(t, ref)
	if err := rt.Start(context.Background()); err == nil || !errors.Is(err, constructionFailure) {
		t.Fatalf("Start did not return the constructor failure")
	}
	if starts.Load() != 0 || stops.Load() != 0 {
		t.Fatalf("hooks ran for a failed constructor: starts=%d stops=%d", starts.Load(), stops.Load())
	}
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("Stop returned an error")
	}
}

func TestStartPanicPreservesConstructedOwnershipAndStack(t *testing.T) {
	var stops atomic.Int32
	ref := component.ProvideValue(func() *runtimeResource {
		return &runtimeResource{
			name: "panic-owner",
			startFn: func(context.Context) error {
				panic("start panic payload")
			},
			stopFn: func(context.Context) error {
				stops.Add(1)
				return nil
			},
		}
	}, component.Managed[*runtimeResource]())
	rt := newTestRuntime(t, ref)
	err := rt.Start(context.Background())
	requireSentinel(t, err, component.ErrPanic)
	ne := nodeError(t, err)
	if ne.Phase != "start" {
		t.Fatalf("panic phase=%q, want start", ne.Phase)
	}
	if len(ne.Stack) == 0 {
		t.Fatalf("panic NodeError has no stack")
	}
	if !strings.Contains(ne.Error(), "start panic payload") {
		t.Fatalf("NodeError text omitted the panic payload")
	}
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("Stop returned an error")
	}
	if stops.Load() != 1 {
		t.Fatalf("stop calls=%d, want 1", stops.Load())
	}
}

func TestConstructorPanicReportsConstructPhase(t *testing.T) {
	ref := component.ProvideValue(func() *runtimeResource {
		panic("construct panic payload")
	})
	rt := newTestRuntime(t, ref)
	err := rt.Start(context.Background())
	requireSentinel(t, err, component.ErrPanic)
	ne := nodeError(t, err)
	if ne.Phase != "construct" || len(ne.Stack) == 0 {
		t.Fatalf("constructor panic NodeError phase=%q stack=%d", ne.Phase, len(ne.Stack))
	}
	if !strings.Contains(ne.Error(), "construct panic payload") {
		t.Fatalf("NodeError text omitted the constructor panic payload")
	}
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("Stop returned an error")
	}
}

func TestStartGoexitPreservesConstructedOwnership(t *testing.T) {
	var stops atomic.Int32
	ref := component.ProvideValue(func() *runtimeResource {
		return &runtimeResource{
			name: "goexit-owner",
			startFn: func(context.Context) error {
				runtime.Goexit()
				return nil
			},
			stopFn: func(context.Context) error {
				stops.Add(1)
				return nil
			},
		}
	}, component.Managed[*runtimeResource]())
	rt := newTestRuntime(t, ref)
	err := rt.Start(context.Background())
	requireSentinel(t, err, component.ErrAborted)
	ne := nodeError(t, err)
	if ne.Phase != "start" {
		t.Fatalf("Goexit phase=%q, want start", ne.Phase)
	}
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("Stop returned an error")
	}
	if stops.Load() != 1 {
		t.Fatalf("stop calls=%d, want 1", stops.Load())
	}
}

func TestFailedStopLeavesNodePendingAndRetainsDependency(t *testing.T) {
	stopFailure := errors.New("child stop failed")
	var childStops, dependencyStops atomic.Int32
	dependency := component.ProvideValue(func() *runtimeResource {
		return &runtimeResource{name: "dependency", stopFn: func(context.Context) error {
			dependencyStops.Add(1)
			return nil
		}}
	}, component.Managed[*runtimeResource]())
	alias := component.MapValue(dependency, func(*runtimeResource) *runtimeResource {
		return &runtimeResource{name: "alias"}
	})
	child := component.MapValue(alias, func(*runtimeResource) *runtimeResource {
		return &runtimeResource{name: "child", stopFn: func(context.Context) error {
			if childStops.Add(1) == 1 {
				return stopFailure
			}
			return nil
		}}
	}, component.Managed[*runtimeResource]())
	rt := newTestRuntime(t, child)
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("Start returned an error")
	}
	first := rt.Stop(context.Background())
	requireSentinel(t, first, component.ErrCleanupPending)
	if !errors.Is(first, stopFailure) {
		t.Fatalf("first Stop omitted the child failure")
	}
	if childStops.Load() != 1 || dependencyStops.Load() != 0 {
		t.Fatalf("first Stop counts: child=%d dependency=%d", childStops.Load(), dependencyStops.Load())
	}
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("retry Stop returned an error")
	}
	if childStops.Load() != 2 || dependencyStops.Load() != 1 {
		t.Fatalf("retry Stop counts: child=%d dependency=%d", childStops.Load(), dependencyStops.Load())
	}
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("terminal Stop returned an error")
	}
	if childStops.Load() != 2 || dependencyStops.Load() != 1 {
		t.Fatalf("terminal Stop retried callbacks")
	}
}

func TestFailedStopContinuesUnrelatedCleanup(t *testing.T) {
	stopFailure := errors.New("branch A stop failed")
	var aStops, bStops atomic.Int32
	a := component.ProvideValue(func() *runtimeResource {
		return &runtimeResource{name: "a", stopFn: func(context.Context) error {
			if aStops.Add(1) == 1 {
				return stopFailure
			}
			return nil
		}}
	}, component.Managed[*runtimeResource]())
	b := component.ProvideValue(func() *runtimeResource {
		return &runtimeResource{name: "b", stopFn: func(context.Context) error {
			bStops.Add(1)
			return nil
		}}
	}, component.Managed[*runtimeResource]())
	rt := newTestRuntime(t, b, a)
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("Start returned an error")
	}
	first := rt.Stop(context.Background())
	requireSentinel(t, first, component.ErrCleanupPending)
	if !errors.Is(first, stopFailure) {
		t.Fatalf("first Stop omitted branch A failure")
	}
	if aStops.Load() != 1 || bStops.Load() != 1 {
		t.Fatalf("first Stop counts: A=%d B=%d, want 1 each", aStops.Load(), bStops.Load())
	}
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("retry Stop returned an error")
	}
	if aStops.Load() != 2 || bStops.Load() != 1 {
		t.Fatalf("retry counts: A=%d B=%d", aStops.Load(), bStops.Load())
	}
}

func TestStopPanicAndGoexitRemainPending(t *testing.T) {
	t.Run("panic", func(t *testing.T) {
		var calls atomic.Int32
		ref := component.ProvideValue(func() *runtimeResource {
			return &runtimeResource{name: "panic-stop", stopFn: func(context.Context) error {
				if calls.Add(1) == 1 {
					panic("stop panic payload")
				}
				return nil
			}}
		}, component.Managed[*runtimeResource]())
		rt := newTestRuntime(t, ref)
		if err := rt.Start(context.Background()); err != nil {
			t.Fatalf("Start returned an error")
		}
		first := rt.Stop(context.Background())
		requireSentinel(t, first, component.ErrPanic)
		requireSentinel(t, first, component.ErrCleanupPending)
		ne := nodeError(t, first)
		if ne.Phase != "stop" || len(ne.Stack) == 0 {
			t.Fatalf("stop panic NodeError phase=%q stack=%d", ne.Phase, len(ne.Stack))
		}
		if err := rt.Stop(context.Background()); err != nil {
			t.Fatalf("retry Stop returned an error")
		}
	})

	t.Run("goexit", func(t *testing.T) {
		var calls atomic.Int32
		ref := component.ProvideValue(func() *runtimeResource {
			return &runtimeResource{name: "goexit-stop", stopFn: func(context.Context) error {
				if calls.Add(1) == 1 {
					runtime.Goexit()
				}
				return nil
			}}
		}, component.Managed[*runtimeResource]())
		rt := newTestRuntime(t, ref)
		if err := rt.Start(context.Background()); err != nil {
			t.Fatalf("Start returned an error")
		}
		first := rt.Stop(context.Background())
		requireSentinel(t, first, component.ErrAborted)
		requireSentinel(t, first, component.ErrCleanupPending)
		if err := rt.Stop(context.Background()); err != nil {
			t.Fatalf("retry Stop returned an error")
		}
	})
}

func TestExactContextsReachFactoriesAndHooks(t *testing.T) {
	startContext := &exactContext{Context: context.Background()}
	stopContext := &exactContext{Context: context.Background()}
	var factorySeen, startSeen, stopSeen context.Context
	ref := component.ProvideContext(func(ctx context.Context) (*runtimeResource, error) {
		factorySeen = ctx
		return &runtimeResource{
			name: "context-owner",
			startFn: func(ctx context.Context) error {
				startSeen = ctx
				return nil
			},
			stopFn: func(ctx context.Context) error {
				stopSeen = ctx
				return nil
			},
		}, nil
	}, component.Managed[*runtimeResource]())
	rt := newTestRuntime(t, ref)
	if err := rt.Start(startContext); err != nil {
		t.Fatalf("Start returned an error")
	}
	if factorySeen != startContext || startSeen != startContext {
		t.Fatalf("startup context was not passed by identity")
	}
	if err := rt.Stop(stopContext); err != nil {
		t.Fatalf("Stop returned an error")
	}
	if stopSeen != stopContext {
		t.Fatalf("shutdown context was not passed by identity")
	}
}

func TestNilContextsDoNotChangeRuntimeState(t *testing.T) {
	var creates, stops atomic.Int32
	ref := component.ProvideValue(func() *runtimeResource {
		creates.Add(1)
		return &runtimeResource{name: "nil-context", stopFn: func(context.Context) error {
			stops.Add(1)
			return nil
		}}
	}, component.Managed[*runtimeResource]())
	rt := newTestRuntime(t, ref)
	//lint:ignore SA1012 nil is the explicit invalid-context contract under test.
	requireSentinel(t, rt.Start(nil), component.ErrInvalidContext)
	if creates.Load() != 0 {
		t.Fatalf("nil Start invoked a factory")
	}
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("valid Start returned an error")
	}
	//lint:ignore SA1012 nil is the explicit invalid-context contract under test.
	requireSentinel(t, rt.Stop(nil), component.ErrInvalidContext)
	if stops.Load() != 0 {
		t.Fatalf("nil Stop invoked a hook")
	}
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("valid Stop returned an error")
	}
}

func TestCanceledStartConsumesTheOnlyStartAttempt(t *testing.T) {
	var creates atomic.Int32
	ref := component.ProvideValue(func() int {
		creates.Add(1)
		return 1
	})
	rt := newTestRuntime(t, ref)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := rt.Start(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Start did not return context cancellation")
	}
	if creates.Load() != 0 {
		t.Fatalf("canceled Start invoked a factory")
	}
	requireSentinel(t, rt.Start(context.Background()), component.ErrAlreadyStarted)
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("Stop after canceled Start returned an error")
	}
}

func TestCancellationDuringFactoryStillRunsItsStartHook(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var starts, stops atomic.Int32
	ref := component.ProvideContext(func(context.Context) (*runtimeResource, error) {
		cancel()
		return &runtimeResource{
			name: "late-cancel",
			startFn: func(context.Context) error {
				starts.Add(1)
				return nil
			},
			stopFn: func(context.Context) error {
				stops.Add(1)
				return nil
			},
		}, nil
	}, component.Managed[*runtimeResource]())
	rt := newTestRuntime(t, ref)
	err := rt.Start(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Start did not report final cancellation")
	}
	if starts.Load() != 1 {
		t.Fatalf("start hook calls=%d, want 1", starts.Load())
	}
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("Stop returned an error")
	}
	if stops.Load() != 1 {
		t.Fatalf("stop hook calls=%d, want 1", stops.Load())
	}
}

func TestCanceledStopWaitsForInFlightCallbackAndRetainsContext(t *testing.T) {
	entered := make(chan struct{})
	release, releaseOnce := makeRelease(t)
	var calls atomic.Int32
	var observedCanceled atomic.Bool
	ancestor := component.Value(7)
	ref := component.MapValue(ancestor, func(int) *runtimeResource {
		return &runtimeResource{name: "stop-context", stopFn: func(ctx context.Context) error {
			// The callback is released only after the caller cancels its context.
			calls.Add(1)
			close(entered)
			<-release
			observedCanceled.Store(errors.Is(ctx.Err(), context.Canceled))
			return nil
		}}
	}, component.Managed[*runtimeResource]())
	rt := newTestRuntime(t, ref)
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("Start returned an error")
	}
	stopContext, cancel := context.WithCancel(context.Background())
	stopResult := make(chan error, 1)
	go func() { stopResult <- rt.Stop(stopContext) }()
	waitSignal(t, entered, "Stop callback did not start")
	cancel()
	releaseOnce()
	if err := waitResult(t, stopResult, "Stop did not await its in-flight callback"); err != nil {
		t.Fatalf("Stop returned an error after callback completed")
	}
	if calls.Load() != 1 {
		t.Fatalf("stop callback calls=%d, want 1", calls.Load())
	}
	if !observedCanceled.Load() {
		t.Fatalf("in-flight Stop callback did not complete after cancellation")
	}
}

func TestCanceledStopDrainsAnAllPureGraph(t *testing.T) {
	ref := component.Value("borrowed")
	rt := newTestRuntime(t, ref)
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("Start returned an error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := rt.Stop(ctx); err != nil {
		t.Fatalf("canceled Stop on an all-pure graph returned an error: %v", err)
	}
	if _, err := rt.Value(ref); err == nil {
		t.Fatalf("Value remained available after canceled Stop")
	} else {
		requireSentinel(t, err, component.ErrUnavailable)
	}
}

func TestCanceledStopBeforeDispatchLeavesCleanupForRetry(t *testing.T) {
	var stops atomic.Int32
	ref := component.ProvideValue(func() *runtimeResource {
		return &runtimeResource{name: "late-stop", stopFn: func(context.Context) error {
			stops.Add(1)
			return nil
		}}
	}, component.Managed[*runtimeResource]())
	rt := newTestRuntime(t, ref)
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("Start returned an error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := rt.Stop(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Stop did not return context cancellation")
	}
	if stops.Load() != 0 {
		t.Fatalf("canceled Stop invoked a callback")
	}
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("retry Stop returned an error")
	}
	if stops.Load() != 1 {
		t.Fatalf("retry stop calls=%d, want 1", stops.Load())
	}
}

func TestOverlappingStartAndStopAreBusy(t *testing.T) {
	entered := make(chan struct{})
	release, releaseOnce := makeRelease(t)
	ref := component.ProvideValue(func() int {
		close(entered)
		<-release
		return 1
	})
	rt := newTestRuntime(t, ref)
	startResult := make(chan error, 1)
	go func() { startResult <- rt.Start(context.Background()) }()
	waitSignal(t, entered, "Start callback did not run")
	requireSentinel(t, rt.Stop(context.Background()), component.ErrBusy)
	releaseOnce()
	if err := waitResult(t, startResult, "Start did not finish"); err != nil {
		t.Fatalf("Start returned an error")
	}
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("Stop returned an error")
	}
}

func TestOverlappingStopsAreBusy(t *testing.T) {
	entered := make(chan struct{})
	release, releaseOnce := makeRelease(t)
	ref := component.ProvideValue(func() *runtimeResource {
		return &runtimeResource{name: "overlap-stop", stopFn: func(context.Context) error {
			close(entered)
			<-release
			return nil
		}}
	}, component.Managed[*runtimeResource]())
	rt := newTestRuntime(t, ref)
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("Start returned an error")
	}
	stopResult := make(chan error, 1)
	go func() { stopResult <- rt.Stop(context.Background()) }()
	waitSignal(t, entered, "first Stop callback did not run")
	requireSentinel(t, rt.Stop(context.Background()), component.ErrBusy)
	releaseOnce()
	if err := waitResult(t, stopResult, "first Stop did not finish"); err != nil {
		t.Fatalf("first Stop returned an error")
	}
}

type panicError struct{}

func (*panicError) Error() string {
	panic("scheduler formatted a panicError")
}

type goexitError struct {
	called atomic.Int32
}

func (e *goexitError) Error() string {
	e.called.Add(1)
	runtime.Goexit()
	return "unreachable"
}

type blockingError struct {
	entered chan struct{}
	release chan struct{}
}

func (e *blockingError) Error() string {
	close(e.entered)
	<-e.release
	return "blocking error"
}

type hostileCause struct{}

func (*hostileCause) Error() string { return "hostile cause" }
func (*hostileCause) Is(error) bool {
	panic("scheduler called hostile Is")
}
func (*hostileCause) As(any) bool {
	panic("scheduler called hostile As")
}
func (*hostileCause) Unwrap() error {
	panic("scheduler called hostile Unwrap")
}

func TestSchedulerDoesNotFormatOrClassifyUserErrors(t *testing.T) {
	t.Run("panic Error", func(t *testing.T) {
		ref := component.Provide(func() (int, error) { return 0, &panicError{} })
		rt := newTestRuntime(t, ref)
		err := rt.Start(context.Background())
		ne := nodeError(t, err)
		if ne.Cause == nil {
			t.Fatalf("NodeError lost the constructor cause")
		}
	})

	t.Run("blocking Error", func(t *testing.T) {
		release, releaseOnce := makeRelease(t)
		hostile := &blockingError{entered: make(chan struct{}), release: release}
		ref := component.Provide(func() (int, error) { return 0, hostile })
		rt := newTestRuntime(t, ref)
		result := make(chan error, 1)
		go func() { result <- rt.Start(context.Background()) }()
		select {
		case <-hostile.entered:
			releaseOnce()
			t.Fatal("scheduler formatted a blocking error")
		case err := <-result:
			if err == nil {
				t.Fatal("Start unexpectedly succeeded")
			}
		case <-time.After(time.Second):
			releaseOnce()
			select {
			case <-result:
			case <-time.After(time.Second):
			}
			t.Fatal("Start did not finish without formatting the error")
		}
	})

	t.Run("Goexit Error", func(t *testing.T) {
		hostile := &goexitError{}
		ref := component.Provide(func() (int, error) { return 0, hostile })
		rt := newTestRuntime(t, ref)
		result := make(chan error, 1)
		go func() { result <- rt.Start(context.Background()) }()
		select {
		case err := <-result:
			if err == nil {
				t.Fatal("Start unexpectedly succeeded")
			}
			if hostile.called.Load() != 0 {
				t.Fatal("scheduler called Error on a user cause")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Start did not finish without formatting the error")
		}
	})

	t.Run("hostile classifiers", func(t *testing.T) {
		cause := &hostileCause{}
		ref := component.Provide(func() (int, error) { return 0, cause })
		rt := newTestRuntime(t, ref)
		err := rt.Start(context.Background())
		ne := nodeError(t, err)
		if ne.Cause != cause {
			t.Fatalf("NodeError changed the opaque cause")
		}
	})
}

func TestConstructorGoexitLeavesPartialCleanupWithFactory(t *testing.T) {
	var localCleanup atomic.Int32
	ref := component.ProvideValue(func() *runtimeResource {
		defer localCleanup.Add(1)
		runtime.Goexit()
		return nil
	})
	rt := newTestRuntime(t, ref)
	err := rt.Start(t.Context())
	requireSentinel(t, err, component.ErrAborted)
	if got := nodeError(t, err).Phase; got != "construct" {
		t.Fatalf("aborted phase = %q, want construct", got)
	}
	if err := rt.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if localCleanup.Load() != 1 {
		t.Fatalf("factory cleanup=%d, want 1", localCleanup.Load())
	}
}

func TestEmptyRuntimeAndStopBeforeStartRemainOneShot(t *testing.T) {
	t.Run("empty graph", func(t *testing.T) {
		rt := newTestRuntime(t)
		if err := rt.Start(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := rt.Stop(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := rt.Stop(t.Context()); err != nil {
			t.Fatal(err)
		}
		requireSentinel(t, rt.Start(t.Context()), component.ErrAlreadyStarted)
	})
	t.Run("stop before start", func(t *testing.T) {
		var calls atomic.Int32
		ref := component.ProvideValue(func() int { calls.Add(1); return 7 })
		rt := newTestRuntime(t, ref)
		if err := rt.Stop(t.Context()); err != nil {
			t.Fatal(err)
		}
		requireSentinel(t, rt.Start(t.Context()), component.ErrAlreadyStarted)
		if calls.Load() != 0 {
			t.Fatalf("factory calls = %d, want 0", calls.Load())
		}
	})
}
