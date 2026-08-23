package component

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	goruntime "runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type eventLog struct {
	mu     sync.Mutex
	events []string
}

func (log *eventLog) add(event string) {
	log.mu.Lock()
	log.events = append(log.events, event)
	log.mu.Unlock()
}

func (log *eventLog) snapshot() []string {
	log.mu.Lock()
	defer log.mu.Unlock()
	return append([]string(nil), log.events...)
}

func recordedLifecycle(log *eventLog, name string) LifecycleFuncs {
	return LifecycleFuncs{
		OnConfigure: func(context.Context) error {
			log.add(name + ".configure")
			return nil
		},
		OnStart: func(context.Context) error {
			log.add(name + ".start")
			return nil
		},
		OnStop: func(context.Context) error {
			log.add(name + ".stop")
			return nil
		},
	}
}

type recordedOwner struct {
	log  *eventLog
	name string
}

func (owner *recordedOwner) Configure(context.Context) error {
	owner.log.add(owner.name + ".configure")
	return nil
}

func (owner *recordedOwner) Start(context.Context) error {
	owner.log.add(owner.name + ".start")
	return nil
}

func (owner *recordedOwner) Stop(context.Context) error {
	owner.log.add(owner.name + ".stop")
	return nil
}

func TestRuntimeOrdersAllConfigureBeforeStartAndStopsInReverse(t *testing.T) {
	t.Parallel()

	log := &eventLog{}
	database := NewNode[*recordedOwner]("database")
	service := NewNode[*recordedOwner]("service")
	registry := NewRegistry()
	if err := registry.Register(service, &recordedOwner{log: log, name: "service"}, database); err != nil {
		t.Fatalf("Register(service) failed: %v", err)
	}
	if err := registry.Register(database, &recordedOwner{log: log, name: "database"}); err != nil {
		t.Fatalf("Register(database) failed: %v", err)
	}
	runtime := mustCompile(t, registry)

	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() failed: %v", err)
	}

	want := []string{
		"database.configure",
		"service.configure",
		"database.start",
		"service.start",
		"service.stop",
		"database.stop",
	}
	if got := log.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
}

func TestConfigureRunsFrontierConcurrentlyAndWaitsBeforeAdvancing(t *testing.T) {
	t.Parallel()

	entered := make(chan string, 2)
	release := make(chan struct{})
	dependentEntered := make(chan struct{})
	rootLifecycle := func(name string) LifecycleFuncs {
		return LifecycleFuncs{OnConfigure: func(context.Context) error {
			entered <- name
			<-release
			return nil
		}}
	}

	first := newTestNode("first")
	second := newTestNode("second")
	dependent := newTestNode("dependent")
	registry := NewRegistry()
	mustRegisterLifecycle(t, registry, dependent, LifecycleFuncs{
		OnConfigure: func(context.Context) error {
			close(dependentEntered)
			return nil
		},
	}, first, second)
	mustRegisterLifecycle(t, registry, second, rootLifecycle("second"))
	mustRegisterLifecycle(t, registry, first, rootLifecycle("first"))
	runtime := mustCompile(t, registry)

	startResult := make(chan error, 1)
	go func() { startResult <- runtime.Start(context.Background()) }()
	for range 2 {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("same-frontier Configure callbacks did not run concurrently")
		}
	}
	select {
	case <-dependentEntered:
		t.Fatal("dependent Configure ran before the root frontier completed")
	default:
	}
	close(release)
	if err := receiveError(t, startResult); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() failed: %v", err)
	}
}

func TestStartRunsFrontierConcurrentlyAndWaitsBeforeAdvancing(t *testing.T) {
	t.Parallel()

	entered := make(chan string, 2)
	release := make(chan struct{})
	dependentEntered := make(chan struct{})
	rootLifecycle := func(name string) LifecycleFuncs {
		return LifecycleFuncs{OnStart: func(context.Context) error {
			entered <- name
			<-release
			return nil
		}}
	}

	first := newTestNode("first")
	second := newTestNode("second")
	dependent := newTestNode("dependent")
	registry := NewRegistry()
	mustRegisterLifecycle(t, registry, dependent, LifecycleFuncs{
		OnStart: func(context.Context) error {
			close(dependentEntered)
			return nil
		},
	}, first, second)
	mustRegisterLifecycle(t, registry, second, rootLifecycle("second"))
	mustRegisterLifecycle(t, registry, first, rootLifecycle("first"))
	runtime := mustCompile(t, registry)

	startResult := make(chan error, 1)
	go func() { startResult <- runtime.Start(context.Background()) }()
	for range 2 {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("same-frontier Start callbacks did not run concurrently")
		}
	}
	select {
	case <-dependentEntered:
		t.Fatal("dependent Start ran before the root frontier completed")
	default:
	}
	close(release)
	if err := receiveError(t, startResult); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() failed: %v", err)
	}
}

func TestConfigureFailureLeavesCleanupForCaller(t *testing.T) {
	t.Parallel()

	configureErr := errors.New("configure failed")
	log := &eventLog{}
	root := newTestNode("root")
	child := newTestNode("child")
	registry := NewRegistry()
	mustRegisterLifecycle(t, registry, root, recordedLifecycle(log, "root"))
	mustRegisterLifecycle(t, registry, child, LifecycleFuncs{
		OnConfigure: func(context.Context) error {
			log.add("child.configure")
			return configureErr
		},
		OnStart: func(context.Context) error {
			log.add("child.start")
			return nil
		},
		OnStop: func(context.Context) error {
			log.add("child.stop")
			return nil
		},
	}, root)
	runtime := mustCompile(t, registry)

	err := runtime.Start(context.Background())
	if !errors.Is(err, configureErr) {
		t.Fatalf("Start() error = %v, want configure cause", err)
	}
	want := []string{
		"root.configure",
		"child.configure",
	}
	if got := log.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	if err := runtime.Start(context.Background()); !errors.Is(err, ErrCleanupPending) {
		t.Fatalf("Start() before cleanup error = %v, want ErrCleanupPending", err)
	}
	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() failed: %v", err)
	}
	want = append(want, "child.stop", "root.stop")
	if got := log.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("events after Stop() = %v, want %v", got, want)
	}
	if err := runtime.Start(context.Background()); !errors.Is(err, ErrAlreadyStarted) {
		t.Fatalf("Start() after cleanup error = %v, want ErrAlreadyStarted", err)
	}
}

func TestStopAfterConfigureFailureCleansInvokedSiblingsOnly(t *testing.T) {
	t.Parallel()

	configureErr := errors.New("configure failed")
	var successfulConfigures atomic.Int32
	var failingConfigures atomic.Int32
	var dependentConfigures atomic.Int32
	var successfulStops atomic.Int32
	var failingStops atomic.Int32
	var dependentStops atomic.Int32

	successful := newTestNode("successful")
	failing := newTestNode("failing")
	dependent := newTestNode("dependent")
	registry := NewRegistry()
	mustRegisterLifecycle(t, registry, successful, LifecycleFuncs{
		OnConfigure: func(context.Context) error { successfulConfigures.Add(1); return nil },
		OnStop:      func(context.Context) error { successfulStops.Add(1); return nil },
	})
	mustRegisterLifecycle(t, registry, failing, LifecycleFuncs{
		OnConfigure: func(context.Context) error { failingConfigures.Add(1); return configureErr },
		OnStop:      func(context.Context) error { failingStops.Add(1); return nil },
	})
	mustRegisterLifecycle(t, registry, dependent, LifecycleFuncs{
		OnConfigure: func(context.Context) error { dependentConfigures.Add(1); return nil },
		OnStop:      func(context.Context) error { dependentStops.Add(1); return nil },
	}, successful, failing)
	runtime := mustCompile(t, registry)

	if err := runtime.Start(context.Background()); !errors.Is(err, configureErr) {
		t.Fatalf("Start() error = %v, want configure cause", err)
	}
	if got := successfulConfigures.Load(); got != 1 {
		t.Fatalf("successful Configure count = %d, want 1", got)
	}
	if got := failingConfigures.Load(); got != 1 {
		t.Fatalf("failing Configure count = %d, want 1", got)
	}
	if got := dependentConfigures.Load(); got != 0 {
		t.Fatalf("unvisited dependent Configure count = %d, want 0", got)
	}
	if got := successfulStops.Load(); got != 0 {
		t.Fatalf("successful sibling Stop count before Stop() = %d, want 0", got)
	}
	if got := failingStops.Load(); got != 0 {
		t.Fatalf("failing sibling Stop count before Stop() = %d, want 0", got)
	}
	if got := dependentStops.Load(); got != 0 {
		t.Fatalf("unvisited dependent Stop count before Stop() = %d, want 0", got)
	}
	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() failed: %v", err)
	}
	if got := successfulStops.Load(); got != 1 {
		t.Fatalf("successful sibling Stop count = %d, want 1", got)
	}
	if got := failingStops.Load(); got != 1 {
		t.Fatalf("failing sibling Stop count = %d, want 1", got)
	}
	if got := dependentStops.Load(); got != 0 {
		t.Fatalf("unvisited dependent Stop count = %d, want 0", got)
	}
}

func TestStopAfterStartFailureCleansConfiguredNodesNeverStarted(t *testing.T) {
	t.Parallel()

	startErr := errors.New("start failed")
	log := &eventLog{}
	a := newTestNode("a")
	b := newTestNode("b")
	c := newTestNode("c")
	registry := NewRegistry()
	mustRegisterLifecycle(t, registry, a, recordedLifecycle(log, "a"))
	bLifecycle := recordedLifecycle(log, "b")
	bLifecycle.OnStart = func(context.Context) error {
		log.add("b.start")
		return startErr
	}
	mustRegisterLifecycle(t, registry, b, bLifecycle, a)
	mustRegisterLifecycle(t, registry, c, recordedLifecycle(log, "c"), b)
	runtime := mustCompile(t, registry)

	err := runtime.Start(context.Background())
	if !errors.Is(err, startErr) {
		t.Fatalf("Start() error = %v, want start cause", err)
	}
	want := []string{
		"a.configure", "b.configure", "c.configure",
		"a.start", "b.start",
	}
	if got := log.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() failed: %v", err)
	}
	want = append(want, "c.stop", "b.stop", "a.stop")
	if got := log.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("events after Stop() = %v, want %v", got, want)
	}
}

func TestStopAfterFailedStartUsesCallerContext(t *testing.T) {
	t.Parallel()

	configureErr := errors.New("configure failed")
	ctx, cancel := context.WithCancel(context.Background())
	stopObserved := make(chan error, 1)
	node := newTestNode("worker")
	registry := NewRegistry()
	mustRegisterLifecycle(t, registry, node, LifecycleFuncs{
		OnConfigure: func(context.Context) error {
			cancel()
			return configureErr
		},
		OnStop: func(ctx context.Context) error {
			_, hasDeadline := ctx.Deadline()
			if !hasDeadline {
				return errors.New("stop context has no deadline")
			}
			stopObserved <- ctx.Err()
			return nil
		},
	})
	runtime := mustCompile(t, registry)

	err := runtime.Start(ctx)
	if !errors.Is(err, configureErr) {
		t.Fatalf("Start() error = %v, want configure cause", err)
	}
	select {
	case got := <-stopObserved:
		t.Fatalf("Start() invoked Stop with context error %v", got)
	default:
	}
	stopCtx, cancelStop := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancelStop()
	if err := runtime.Stop(stopCtx); err != nil {
		t.Fatalf("Stop() failed: %v", err)
	}
	if got := receiveError(t, stopObserved); got != nil {
		t.Fatalf("Stop() context error = %v, want nil", got)
	}
}

func TestStopTimeoutAfterFailedStartRetainsCleanupForRetry(t *testing.T) {
	t.Parallel()

	configureErr := errors.New("configure failed")
	var stops atomic.Int32
	node := newTestNode("worker")
	registry := NewRegistry()
	mustRegisterLifecycle(t, registry, node, LifecycleFuncs{
		OnConfigure: func(context.Context) error { return configureErr },
		OnStop: func(ctx context.Context) error {
			if stops.Add(1) == 1 {
				<-ctx.Done()
				return ctx.Err()
			}
			return nil
		},
	})
	runtime := mustCompile(t, registry)

	err := runtime.Start(context.Background())
	if !errors.Is(err, configureErr) {
		t.Fatalf("Start() error = %v, want configure cause", err)
	}
	if got := stops.Load(); got != 0 {
		t.Fatalf("Stop call count before Stop() = %d, want 0", got)
	}
	stopCtx, cancelStop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelStop()
	if err := runtime.Stop(stopCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stop() error = %v, want context.DeadlineExceeded", err)
	}
	if err := runtime.Start(context.Background()); !errors.Is(err, ErrCleanupPending) {
		t.Fatalf("Start() with timed-out cleanup error = %v, want ErrCleanupPending", err)
	}
	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatalf("retry Stop() failed: %v", err)
	}
	if err := runtime.Start(context.Background()); !errors.Is(err, ErrAlreadyStarted) {
		t.Fatalf("Start() after completed retry error = %v, want ErrAlreadyStarted", err)
	}
}

func TestStopFailureBlocksOnlyItsDependencyAndCanBeRetried(t *testing.T) {
	t.Parallel()

	stopErr := errors.New("leaf-a stop failed")
	var dependencyAStops atomic.Int32
	var leafAStops atomic.Int32
	var dependencyBStops atomic.Int32
	var leafBStops atomic.Int32

	dependencyA := newTestNode("dependency-a")
	leafA := newTestNode("leaf-a")
	dependencyB := newTestNode("dependency-b")
	leafB := newTestNode("leaf-b")
	registry := NewRegistry()
	mustRegisterLifecycle(t, registry, dependencyA, LifecycleFuncs{
		OnStop: func(context.Context) error { dependencyAStops.Add(1); return nil },
	})
	mustRegisterLifecycle(t, registry, leafA, LifecycleFuncs{
		OnStop: func(context.Context) error {
			if leafAStops.Add(1) == 1 {
				return stopErr
			}
			return nil
		},
	}, dependencyA)
	mustRegisterLifecycle(t, registry, dependencyB, LifecycleFuncs{
		OnStop: func(context.Context) error { dependencyBStops.Add(1); return nil },
	})
	mustRegisterLifecycle(t, registry, leafB, LifecycleFuncs{
		OnStop: func(context.Context) error { leafBStops.Add(1); return nil },
	}, dependencyB)
	runtime := mustCompile(t, registry)
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	if err := runtime.Stop(context.Background()); !errors.Is(err, stopErr) {
		t.Fatalf("first Stop() error = %v, want leaf failure", err)
	}
	if got := dependencyAStops.Load(); got != 0 {
		t.Fatalf("blocked dependency-a Stop count = %d, want 0", got)
	}
	if got := dependencyBStops.Load(); got != 1 {
		t.Fatalf("unrelated dependency-b Stop count = %d, want 1", got)
	}
	if err := runtime.Start(context.Background()); !errors.Is(err, ErrCleanupPending) {
		t.Fatalf("Start() with pending cleanup error = %v, want ErrCleanupPending", err)
	}

	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatalf("retry Stop() failed: %v", err)
	}
	if got := leafAStops.Load(); got != 2 {
		t.Fatalf("leaf-a Stop count = %d, want 2", got)
	}
	if got := dependencyAStops.Load(); got != 1 {
		t.Fatalf("dependency-a Stop count = %d, want 1", got)
	}
	if got := leafBStops.Load(); got != 1 {
		t.Fatalf("leaf-b was stopped again: count = %d", got)
	}
	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatalf("idempotent Stop() failed: %v", err)
	}
}

func TestStopFailureBlocksSharedDependencyUntilEveryDependentStops(t *testing.T) {
	t.Parallel()

	stopErr := errors.New("alpha stop failed")
	var alphaStops atomic.Int32
	var zetaStops atomic.Int32
	var sharedStops atomic.Int32
	shared := newTestNode("shared")
	alpha := newTestNode("alpha")
	zeta := newTestNode("zeta")
	registry := NewRegistry()
	mustRegisterLifecycle(t, registry, shared, LifecycleFuncs{
		OnStop: func(context.Context) error { sharedStops.Add(1); return nil },
	})
	mustRegisterLifecycle(t, registry, alpha, LifecycleFuncs{
		OnStop: func(context.Context) error {
			if alphaStops.Add(1) == 1 {
				return stopErr
			}
			return nil
		},
	}, shared)
	mustRegisterLifecycle(t, registry, zeta, LifecycleFuncs{
		OnStop: func(context.Context) error { zetaStops.Add(1); return nil },
	}, shared)
	runtime := mustCompile(t, registry)
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	if err := runtime.Stop(context.Background()); !errors.Is(err, stopErr) {
		t.Fatalf("first Stop() error = %v, want alpha failure", err)
	}
	if got := sharedStops.Load(); got != 0 {
		t.Fatalf("shared dependency Stop count = %d, want 0", got)
	}
	if got := zetaStops.Load(); got != 1 {
		t.Fatalf("successful dependent Stop count = %d, want 1", got)
	}
	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatalf("retry Stop() failed: %v", err)
	}
	if got := sharedStops.Load(); got != 1 {
		t.Fatalf("shared dependency Stop count after retry = %d, want 1", got)
	}
	if got := zetaStops.Load(); got != 1 {
		t.Fatalf("successful dependent was retried: count = %d", got)
	}
}

func TestStopAdvancesSuccessfulBranchWhileUnrelatedCallbackRuns(t *testing.T) {
	t.Parallel()

	slowEntered := make(chan struct{})
	releaseSlow := make(chan struct{})
	fastDependencyStopped := make(chan struct{})
	fastDependency := newTestNode("fast-dependency")
	fastLeaf := newTestNode("fast-leaf")
	slowLeaf := newTestNode("slow-leaf")
	registry := NewRegistry()
	mustRegisterLifecycle(t, registry, fastDependency, LifecycleFuncs{
		OnStop: func(context.Context) error {
			close(fastDependencyStopped)
			return nil
		},
	})
	mustRegisterLifecycle(t, registry, fastLeaf, LifecycleFuncs{}, fastDependency)
	mustRegisterLifecycle(t, registry, slowLeaf, LifecycleFuncs{
		OnStop: func(context.Context) error {
			close(slowEntered)
			<-releaseSlow
			return nil
		},
	})
	runtime := mustCompile(t, registry)
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	stopResult := make(chan error, 1)
	go func() { stopResult <- runtime.Stop(context.Background()) }()
	select {
	case <-slowEntered:
	case <-time.After(time.Second):
		t.Fatal("slow Stop callback did not start")
	}
	select {
	case <-fastDependencyStopped:
		// The successful branch advanced without waiting for slowLeaf.
	case <-time.After(time.Second):
		t.Fatal("fast dependency did not stop while unrelated callback was running")
	}
	close(releaseSlow)
	if err := receiveError(t, stopResult); err != nil {
		t.Fatalf("Stop() failed: %v", err)
	}
}

func TestLifecycleErrorsAreAggregatedDeterministically(t *testing.T) {
	t.Parallel()

	alphaErr := errors.New("alpha failure")
	zetaErr := errors.New("zeta failure")
	registry := NewRegistry()
	mustRegisterLifecycle(t, registry, newTestNode("zeta"), LifecycleFuncs{
		OnStop: func(context.Context) error { return zetaErr },
	})
	mustRegisterLifecycle(t, registry, newTestNode("alpha"), LifecycleFuncs{
		OnStop: func(context.Context) error { return alphaErr },
	})
	runtime := mustCompile(t, registry)
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	err := runtime.Stop(context.Background())
	if !errors.Is(err, alphaErr) || !errors.Is(err, zetaErr) {
		t.Fatalf("Stop() error = %v, want both causes", err)
	}
	alphaPosition := strings.Index(err.Error(), alphaErr.Error())
	zetaPosition := strings.Index(err.Error(), zetaErr.Error())
	if alphaPosition < 0 || zetaPosition < 0 || alphaPosition >= zetaPosition {
		t.Fatalf("Stop() error order is not alpha then zeta: %q", err)
	}
}

func TestConfigureAggregatesConcurrentFailuresDeterministically(t *testing.T) {
	t.Parallel()

	alphaErr := errors.New("alpha configure failure")
	zetaErr := errors.New("zeta configure failure")
	registry := NewRegistry()
	mustRegisterLifecycle(t, registry, newTestNode("zeta"), LifecycleFuncs{
		OnConfigure: func(context.Context) error { return zetaErr },
	})
	mustRegisterLifecycle(t, registry, newTestNode("alpha"), LifecycleFuncs{
		OnConfigure: func(context.Context) error { return alphaErr },
	})
	runtime := mustCompile(t, registry)

	err := runtime.Start(context.Background())
	if !errors.Is(err, alphaErr) || !errors.Is(err, zetaErr) {
		t.Fatalf("Start() error = %v, want both configure causes", err)
	}
	alphaPosition := strings.Index(err.Error(), alphaErr.Error())
	zetaPosition := strings.Index(err.Error(), zetaErr.Error())
	if alphaPosition < 0 || zetaPosition < 0 || alphaPosition >= zetaPosition {
		t.Fatalf("Start() error order is not alpha then zeta: %q", err)
	}
}

func TestStopFailureAfterFailedStartRetainsCleanupForRetry(t *testing.T) {
	t.Parallel()

	configureErr := errors.New("configure failure")
	stopErr := errors.New("stop failure")
	var stops atomic.Int32
	runtime := runtimeWithOneNode(t, LifecycleFuncs{
		OnConfigure: func(context.Context) error { return configureErr },
		OnStop: func(context.Context) error {
			if stops.Add(1) == 1 {
				return stopErr
			}
			return nil
		},
	})

	err := runtime.Start(context.Background())
	if !errors.Is(err, configureErr) {
		t.Fatalf("Start() error = %v, want configure cause", err)
	}
	if got := stops.Load(); got != 0 {
		t.Fatalf("Stop call count before Stop() = %d, want 0", got)
	}
	if err := runtime.Stop(context.Background()); !errors.Is(err, stopErr) {
		t.Fatalf("first Stop() error = %v, want stop cause", err)
	}
	if err := runtime.Start(context.Background()); !errors.Is(err, ErrCleanupPending) {
		t.Fatalf("Start() with retained cleanup error = %v, want ErrCleanupPending", err)
	}
	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatalf("retry Stop() failed: %v", err)
	}
	if got := stops.Load(); got != 2 {
		t.Fatalf("Stop call count = %d, want 2", got)
	}
	if err := runtime.Start(context.Background()); !errors.Is(err, ErrAlreadyStarted) {
		t.Fatalf("Start() after completed cleanup error = %v, want ErrAlreadyStarted", err)
	}
}

func TestRuntimeIsOneShot(t *testing.T) {
	t.Parallel()

	t.Run("after successful start", func(t *testing.T) {
		t.Parallel()

		var configureCalls atomic.Int32
		var startCalls atomic.Int32
		var stopCalls atomic.Int32
		runtime := runtimeWithOneNode(t, LifecycleFuncs{
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
		})
		if err := runtime.Start(context.Background()); err != nil {
			t.Fatalf("Start() failed: %v", err)
		}
		if err := runtime.Start(context.Background()); !errors.Is(err, ErrAlreadyStarted) {
			t.Fatalf("second Start() error = %v, want ErrAlreadyStarted", err)
		}
		if err := runtime.Stop(context.Background()); err != nil {
			t.Fatalf("Stop() failed: %v", err)
		}
		if err := runtime.Start(context.Background()); !errors.Is(err, ErrAlreadyStarted) {
			t.Fatalf("Start() after Stop() error = %v, want ErrAlreadyStarted", err)
		}
		if got := configureCalls.Load(); got != 1 {
			t.Fatalf("Configure call count = %d, want 1", got)
		}
		if got := startCalls.Load(); got != 1 {
			t.Fatalf("Start call count = %d, want 1", got)
		}
		if got := stopCalls.Load(); got != 1 {
			t.Fatalf("Stop call count = %d, want 1", got)
		}
	})

	t.Run("after stop before start", func(t *testing.T) {
		t.Parallel()

		var stopCalls atomic.Int32
		runtime := runtimeWithOneNode(t, LifecycleFuncs{
			OnStop: func(context.Context) error { stopCalls.Add(1); return nil },
		})
		if err := runtime.Stop(context.Background()); err != nil {
			t.Fatalf("Stop() failed: %v", err)
		}
		if got := stopCalls.Load(); got != 0 {
			t.Fatalf("Stop before Start invoked callbacks %d times", got)
		}
		if err := runtime.Start(context.Background()); !errors.Is(err, ErrAlreadyStarted) {
			t.Fatalf("Start() after Stop() error = %v, want ErrAlreadyStarted", err)
		}
	})
}

func TestCopiedRuntimeSharesOneShotState(t *testing.T) {
	t.Parallel()

	var starts atomic.Int32
	runtime := runtimeWithOneNode(t, LifecycleFuncs{
		OnStart: func(context.Context) error { starts.Add(1); return nil },
	})
	copied := *runtime
	if err := copied.Start(context.Background()); err != nil {
		t.Fatalf("Start() through copy failed: %v", err)
	}
	if err := runtime.Start(context.Background()); !errors.Is(err, ErrAlreadyStarted) {
		t.Fatalf("Start() through original error = %v, want ErrAlreadyStarted", err)
	}
	if got := starts.Load(); got != 1 {
		t.Fatalf("Start callback count = %d, want 1", got)
	}
	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() through original failed: %v", err)
	}
	if err := copied.Stop(context.Background()); err != nil {
		t.Fatalf("idempotent Stop() through copy failed: %v", err)
	}
}

func TestZeroRuntimeIsInvalid(t *testing.T) {
	t.Parallel()

	var runtime Runtime
	if err := runtime.Start(context.Background()); err == nil {
		t.Fatal("zero Runtime Start() succeeded")
	}
	if err := runtime.Stop(context.Background()); err == nil {
		t.Fatal("zero Runtime Stop() succeeded")
	}
}

func TestRuntimeRejectsOverlappingOperations(t *testing.T) {
	t.Parallel()

	entered := make(chan struct{})
	release := make(chan struct{})
	runtime := runtimeWithOneNode(t, LifecycleFuncs{
		OnConfigure: func(context.Context) error {
			close(entered)
			<-release
			return nil
		},
	})
	startResult := make(chan error, 1)
	go func() { startResult <- runtime.Start(context.Background()) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("Configure did not start")
	}
	if err := runtime.Start(context.Background()); !errors.Is(err, ErrRuntimeBusy) {
		t.Fatalf("overlapping Start() error = %v, want ErrRuntimeBusy", err)
	}
	if err := runtime.Stop(context.Background()); !errors.Is(err, ErrRuntimeBusy) {
		t.Fatalf("overlapping Stop() error = %v, want ErrRuntimeBusy", err)
	}
	close(release)
	if err := receiveError(t, startResult); err != nil {
		t.Fatalf("original Start() failed: %v", err)
	}
	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatalf("final Stop() failed: %v", err)
	}
}

func TestStopRunsEligibleNodesConcurrentlyAndRejectsOverlap(t *testing.T) {
	t.Parallel()

	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	stopLifecycle := func() LifecycleFuncs {
		return LifecycleFuncs{OnStop: func(context.Context) error {
			entered <- struct{}{}
			<-release
			return nil
		}}
	}
	registry := NewRegistry()
	mustRegisterLifecycle(t, registry, newTestNode("alpha"), stopLifecycle())
	mustRegisterLifecycle(t, registry, newTestNode("zeta"), stopLifecycle())
	runtime := mustCompile(t, registry)
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	stopResult := make(chan error, 1)
	go func() { stopResult <- runtime.Stop(context.Background()) }()
	for range 2 {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("eligible Stop callbacks did not run concurrently")
		}
	}
	if err := runtime.Stop(context.Background()); !errors.Is(err, ErrRuntimeBusy) {
		t.Fatalf("overlapping Stop() error = %v, want ErrRuntimeBusy", err)
	}
	if err := runtime.Start(context.Background()); !errors.Is(err, ErrRuntimeBusy) {
		t.Fatalf("Start() during Stop() error = %v, want ErrRuntimeBusy", err)
	}
	close(release)
	if err := receiveError(t, stopResult); err != nil {
		t.Fatalf("original Stop() failed: %v", err)
	}
}

func TestRuntimeWaitsForCallbackAfterContextCancellation(t *testing.T) {
	t.Parallel()

	entered := make(chan struct{})
	release := make(chan struct{})
	var stops atomic.Int32
	runtime := runtimeWithOneNode(t, LifecycleFuncs{
		OnStart: func(context.Context) error {
			close(entered)
			<-release
			return nil
		},
		OnStop: func(context.Context) error { stops.Add(1); return nil },
	})
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- runtime.Start(ctx) }()
	<-entered
	cancel()
	select {
	case err := <-result:
		t.Fatalf("Start() returned before its callback completed: %v", err)
	default:
	}
	close(release)
	err := receiveError(t, result)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Start() error = %v, want context.Canceled", err)
	}
	if got := stops.Load(); got != 0 {
		t.Fatalf("Stop count before Stop() = %d, want 0", got)
	}
	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() failed: %v", err)
	}
	if got := stops.Load(); got != 1 {
		t.Fatalf("Stop count = %d, want 1", got)
	}
}

func TestStopAfterConfigureCancellationUsesCallerContext(t *testing.T) {
	t.Parallel()

	entered := make(chan struct{})
	stopContextErr := make(chan error, 1)
	runtime := runtimeWithOneNode(t, LifecycleFuncs{
		OnConfigure: func(ctx context.Context) error {
			close(entered)
			<-ctx.Done()
			return ctx.Err()
		},
		OnStop: func(ctx context.Context) error {
			stopContextErr <- ctx.Err()
			return nil
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- runtime.Start(ctx) }()
	<-entered
	cancel()
	err := receiveError(t, result)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Start() error = %v, want context.Canceled", err)
	}
	select {
	case got := <-stopContextErr:
		t.Fatalf("Start() invoked Stop with context error %v", got)
	default:
	}
	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() failed: %v", err)
	}
	if got := receiveError(t, stopContextErr); got != nil {
		t.Fatalf("Stop() context error = %v, want nil", got)
	}
}

func TestCancelledStopRetainsNodeForRetry(t *testing.T) {
	t.Parallel()

	var stops atomic.Int32
	runtime := runtimeWithOneNode(t, LifecycleFuncs{
		OnStop: func(ctx context.Context) error {
			stops.Add(1)
			return ctx.Err()
		},
	})
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runtime.Stop(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("Stop(cancelled) error = %v, want context.Canceled", err)
	}
	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatalf("retry Stop() failed: %v", err)
	}
	if got := stops.Load(); got != 2 {
		t.Fatalf("Stop call count = %d, want 2", got)
	}
}

func TestLifecyclePanicsAreCapturedWithCauseAndStack(t *testing.T) {
	t.Parallel()

	panicCause := errors.New("broken callback")
	var stops atomic.Int32
	runtime := runtimeWithOneNode(t, LifecycleFuncs{
		OnConfigure: func(context.Context) error { panic(panicCause) },
		OnStop:      func(context.Context) error { stops.Add(1); return nil },
	})
	err := runtime.Start(context.Background())
	if !errors.Is(err, ErrPanic) || !errors.Is(err, panicCause) {
		t.Fatalf("Start() error = %v, want ErrPanic and panic cause", err)
	}
	if !strings.Contains(err.Error(), "phase configure") ||
		!strings.Contains(err.Error(), "goroutine") {
		t.Fatalf("panic error lacks phase or stack: %q", err)
	}
	if got := stops.Load(); got != 0 {
		t.Fatalf("Stop count before Stop() = %d, want 0", got)
	}
	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() failed: %v", err)
	}
	if got := stops.Load(); got != 1 {
		t.Fatalf("Stop count = %d, want 1", got)
	}
}

func TestStartPanicLeavesConfiguredGraphForCallerCleanup(t *testing.T) {
	t.Parallel()

	panicCause := errors.New("start panic")
	var stops atomic.Int32
	runtime := runtimeWithOneNode(t, LifecycleFuncs{
		OnStart: func(context.Context) error { panic(panicCause) },
		OnStop:  func(context.Context) error { stops.Add(1); return nil },
	})
	err := runtime.Start(context.Background())
	if !errors.Is(err, ErrPanic) || !errors.Is(err, panicCause) {
		t.Fatalf("Start() error = %v, want ErrPanic and panic cause", err)
	}
	if !strings.Contains(err.Error(), "phase start") {
		t.Fatalf("panic error lacks start phase: %q", err)
	}
	if got := stops.Load(); got != 0 {
		t.Fatalf("Stop count before Stop() = %d, want 0", got)
	}
	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() failed: %v", err)
	}
	if got := stops.Load(); got != 1 {
		t.Fatalf("Stop count = %d, want 1", got)
	}
}

func TestStopPanicRetainsCleanupForRetry(t *testing.T) {
	t.Parallel()

	panicCause := errors.New("stop panic")
	var stops atomic.Int32
	runtime := runtimeWithOneNode(t, LifecycleFuncs{
		OnStop: func(context.Context) error {
			if stops.Add(1) == 1 {
				panic(panicCause)
			}
			return nil
		},
	})
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	err := runtime.Stop(context.Background())
	if !errors.Is(err, ErrPanic) || !errors.Is(err, panicCause) {
		t.Fatalf("Stop() error = %v, want ErrPanic and panic cause", err)
	}
	if !strings.Contains(err.Error(), "phase stop") {
		t.Fatalf("panic error lacks stop phase: %q", err)
	}
	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatalf("retry Stop() failed: %v", err)
	}
}

func TestLifecycleGoexitIsCapturedAndCleanupIsRetryable(t *testing.T) {
	t.Parallel()

	var stopCalls atomic.Int32
	runtime := runtimeWithOneNode(t, LifecycleFuncs{
		OnStop: func(context.Context) error {
			if stopCalls.Add(1) == 1 {
				goruntime.Goexit()
			}
			return nil
		},
	})
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	if err := runtime.Stop(context.Background()); !errors.Is(err, ErrLifecycleAborted) {
		t.Fatalf("first Stop() error = %v, want ErrLifecycleAborted", err)
	}
	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatalf("retry Stop() failed: %v", err)
	}
	if got := stopCalls.Load(); got != 2 {
		t.Fatalf("Stop call count = %d, want 2", got)
	}
}

func TestStartGoexitLeavesConfiguredGraphForCallerCleanup(t *testing.T) {
	t.Parallel()

	var stops atomic.Int32
	runtime := runtimeWithOneNode(t, LifecycleFuncs{
		OnStart: func(context.Context) error {
			goruntime.Goexit()
			return nil
		},
		OnStop: func(context.Context) error { stops.Add(1); return nil },
	})
	err := runtime.Start(context.Background())
	if !errors.Is(err, ErrLifecycleAborted) {
		t.Fatalf("Start() error = %v, want ErrLifecycleAborted", err)
	}
	if got := stops.Load(); got != 0 {
		t.Fatalf("Stop count before Stop() = %d, want 0", got)
	}
	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() failed: %v", err)
	}
	if got := stops.Load(); got != 1 {
		t.Fatalf("Stop count = %d, want 1", got)
	}
}

func TestConfigureGoexitLeavesInvokedNodeForCallerCleanup(t *testing.T) {
	t.Parallel()

	var stops atomic.Int32
	runtime := runtimeWithOneNode(t, LifecycleFuncs{
		OnConfigure: func(context.Context) error {
			goruntime.Goexit()
			return nil
		},
		OnStop: func(context.Context) error { stops.Add(1); return nil },
	})
	err := runtime.Start(context.Background())
	if !errors.Is(err, ErrLifecycleAborted) {
		t.Fatalf("Start() error = %v, want ErrLifecycleAborted", err)
	}
	if got := stops.Load(); got != 0 {
		t.Fatalf("Stop count before Stop() = %d, want 0", got)
	}
	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() failed: %v", err)
	}
	if got := stops.Load(); got != 1 {
		t.Fatalf("Stop count = %d, want 1", got)
	}
}

func TestStopHandlesDeepCleanupGraphIteratively(t *testing.T) {
	if testing.Short() {
		t.Skip("deep graph")
	}

	const nodeCount = 10_000
	registry := NewRegistry()
	var previous testNode
	for index := range nodeCount {
		node := newTestNode(fmt.Sprintf("cleanup-%05d", index))
		if index == 0 {
			mustRegisterLifecycle(t, registry, node, LifecycleFuncs{})
		} else {
			mustRegisterLifecycle(t, registry, node, LifecycleFuncs{}, previous)
		}
		previous = node
	}
	runtime := mustCompile(t, registry)

	// Isolate the cleanup scheduler from 20,000 no-op Configure/Start calls.
	runtime.core.mu.Lock()
	for index := range runtime.core.cleanup {
		runtime.core.cleanup[index] = true
	}
	runtime.core.state = runtimeRunning
	runtime.core.mu.Unlock()
	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() failed: %v", err)
	}
}

func mustCompile(t *testing.T, registry *Registry) *Runtime {
	t.Helper()
	runtime, err := registry.Compile()
	if err != nil {
		t.Fatalf("Compile() failed: %v", err)
	}
	return runtime
}

func mustRegisterLifecycle(
	t *testing.T,
	registry *Registry,
	node testNode,
	lifecycle Lifecycle,
	dependencies ...NodeRef,
) {
	t.Helper()
	if err := registry.Register(node, lifecycle, dependencies...); err != nil {
		t.Fatalf("Register(%q) failed: %v", node, err)
	}
}

func runtimeWithOneNode(t *testing.T, lifecycle Lifecycle) *Runtime {
	t.Helper()
	registry := NewRegistry()
	mustRegisterLifecycle(t, registry, newTestNode("worker"), lifecycle)
	return mustCompile(t, registry)
}

func receiveError(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for lifecycle operation")
		return nil
	}
}
