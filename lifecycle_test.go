package component_test

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"

	"github.com/jacoelho/component"
)

// diamond is cfg <- db, cfg <- cache, (db, cache) <- api, each node recorded.
func diamond(rec *recorder) component.Ref[string] {
	cfg := component.ProvideValue(func() int { return 1 }, hooks[int](rec, "cfg")...)
	db := component.MapValue(cfg, func(int) *database { return &database{} }, hooks[*database](rec, "db")...)
	cache := component.MapValue(cfg, func(int) uint { return 2 }, hooks[uint](rec, "cache")...)
	return component.MapValue2(db, cache, func(*database, uint) string { return "api" }, hooks[string](rec, "api")...)
}

func TestStartAndStopFollowTheGraph(t *testing.T) {
	rec := &recorder{}
	app := mustNew(t, []component.Root{diamond(rec)})
	if events := rec.list(); len(events) != 0 {
		t.Fatalf("New ran hooks: %q", events)
	}

	if err := app.Start(testContext(t)); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := app.Stop(testContext(t)); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	events := rec.list()
	for _, edge := range [][2]string{{"cfg", "db"}, {"cfg", "cache"}, {"db", "api"}, {"cache", "api"}} {
		dep, user := edge[0], edge[1]
		requireBefore(t, events, "start "+dep, "start "+user)
		requireBefore(t, events, "stop "+user, "stop "+dep)
	}
	for _, n := range []string{"cfg", "db", "cache", "api"} {
		// Every drain runs before any node stops.
		requireBefore(t, events, "drain "+n, "stop api")
		requireBefore(t, events, "stop "+n, "close "+n)
	}
}

func TestStartIsSingleUse(t *testing.T) {
	app := mustNew(t, []component.Root{component.Value(1)})
	if err := app.Start(testContext(t)); err != nil {
		t.Fatalf("Start: %v", err)
	}
	requireIs(t, app.Start(testContext(t)), component.ErrStarted)
	if err := app.Stop(testContext(t)); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	requireIs(t, app.Start(testContext(t)), component.ErrStopped)

	stopped := mustNew(t, []component.Root{component.Value(1)})
	if err := stopped.Stop(testContext(t)); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	requireIs(t, stopped.Start(testContext(t)), component.ErrStopped)
}

func TestStartFailureLeavesCleanupToStop(t *testing.T) {
	rec := &recorder{}
	refused := errors.New("refused")
	cfg := component.ProvideValue(func() int { return 1 }, hooks[int](rec, "cfg")...)
	db := component.MapValue(cfg, func(int) string { return "db" }, append(hooks[string](rec, "db"),
		component.OnStart(func(string, context.Context) error { rec.add("ping db"); return refused }))...)
	api := component.MapValue(db, func(string) bool { return true }, hooks[bool](rec, "api")...)
	app := mustNew(t, []component.Root{api})

	err := app.Start(testContext(t))
	requireNodeError(t, err, "db", component.PhaseStart, refused)
	events := rec.list()
	requireAbsent(t, events, "start api")
	requireAbsent(t, events, "stop cfg") // no rollback inside Start

	if err := app.Stop(testContext(t)); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	events = rec.list()
	// db's start began, so it is drained and stopped; api never started.
	requireBefore(t, events, "start db", "ping db")
	requireBefore(t, events, "stop db", "stop cfg")
	requireAbsent(t, events, "stop api")
	requireAbsent(t, events, "drain api")
	requireOnce(t, events, "close api")
}

func TestStartStopsDispatchWhenItsContextIsDone(t *testing.T) {
	rec := &recorder{}
	ctx, cancel := context.WithCancelCause(testContext(t))
	cause := errors.New("signal")
	first := component.ProvideValue(func() int { return 1 }, append(hooks[int](rec, "first"),
		component.OnStart(func(int, context.Context) error { cancel(cause); return nil }))...)
	second := component.MapValue(first, func(int) uint { return 2 }, hooks[uint](rec, "second")...)
	app := mustNew(t, []component.Root{second})

	err := app.Start(ctx)
	requireIs(t, err, cause)
	// No node failed: the cancellation is not attributed to one.
	if ne := (*component.NodeError)(nil); errors.As(err, &ne) {
		t.Fatalf("Start blamed node %q for its own cancelled context: %v", ne.Label, err)
	}
	requireAbsent(t, rec.list(), "start second")
	if err := app.Stop(testContext(t)); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// server models a resource whose task returns only when a stop hook asks it
// to, like http.Server's Serve and Shutdown.
type server struct {
	rec  *recorder
	quit chan struct{}
}

func (s *server) run(ctx context.Context) error {
	<-s.quit
	s.rec.add("run returned; ctx done=" + boolString(ctx.Err() != nil))
	return nil
}

func boolString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func TestStopRunsHooksBeforeAwaitingTasks(t *testing.T) {
	rec := &recorder{}
	srv := component.ProvideValue(func() *server { return &server{rec: rec, quit: make(chan struct{})} },
		component.Go((*server).run),
		component.OnDrain(func(*server, context.Context) error { rec.add("drain"); return nil }),
		component.OnClose(func(*server) error { rec.add("close"); return nil }),
		component.OnStop(func(s *server, ctx context.Context) error {
			rec.add("shutdown")
			close(s.quit)
			return nil
		}))
	app := mustNew(t, []component.Root{srv})
	if err := app.Start(testContext(t)); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := app.Stop(testContext(t)); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	events := rec.list()
	requireBefore(t, events, "drain", "shutdown")
	requireBefore(t, events, "shutdown", "close") // reverse option order
	requireOnce(t, events, "run returned; ctx done=true")
}

func TestTaskContextIsCancelledOnlyAtItsNodesTurn(t *testing.T) {
	rec := &recorder{}
	worker := component.ProvideValue(func() int { return 1 },
		component.Label[int]("worker"),
		component.Go(func(_ int, ctx context.Context) error {
			<-ctx.Done()
			rec.add("worker cancelled")
			return ctx.Err()
		}),
		component.OnStop(func(int, context.Context) error { rec.add("stop worker"); return nil }))
	api := component.MapValue(worker, func(int) string { return "api" },
		component.OnDrain(func(string, context.Context) error { rec.add("drain api"); return nil }),
		component.OnStop(func(string, context.Context) error { rec.add("stop api"); return nil }))
	app := mustNew(t, []component.Root{api})
	startCtx, cancelStart := context.WithCancel(testContext(t))
	if err := app.Start(startCtx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	cancelStart() // the task's context does not follow the start context

	// A task returning its context's error after cancellation is a clean exit.
	if err := app.Stop(testContext(t)); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	events := rec.list()
	requireBefore(t, events, "drain api", "worker cancelled")
	requireBefore(t, events, "stop api", "worker cancelled")
}

func TestStopContinuesAfterErrorsAndRunsEachHookOnce(t *testing.T) {
	rec := &recorder{}
	errDrain, errStop, errClose, errTask := errors.New("drain"), errors.New("stop"), errors.New("close"), errors.New("task")
	db := component.ProvideValue(func() int { return 1 }, append(hooks[int](rec, "db"),
		component.OnClose(func(int) error { return errClose }))...)
	api := component.MapValue(db, func(int) string { return "api" },
		component.Label[string]("api"),
		component.Go(func(_ string, ctx context.Context) error { <-ctx.Done(); return errTask }),
		component.OnDrain(func(string, context.Context) error { rec.add("drain api"); return errDrain }),
		component.OnStop(func(string, context.Context) error { rec.add("stop api"); return errStop }),
		component.OnStop(func(string, context.Context) error { panic("second stop hook") }))
	app := mustNew(t, []component.Root{api})
	if err := app.Start(testContext(t)); err != nil {
		t.Fatalf("Start: %v", err)
	}

	var wg sync.WaitGroup
	errs := make([]error, 3)
	for i := range errs {
		wg.Go(func() { errs[i] = app.Stop(testContext(t)) })
	}
	wg.Wait()

	err := errs[0]
	requireNodeError(t, err, "api", component.PhaseDrain, errDrain)
	requireNodeError(t, err, "api", component.PhaseStop, errStop)
	requireNodeError(t, err, "api", component.PhaseStop, component.ErrPanic)
	requireNodeError(t, err, "api", component.PhaseTask, errTask)
	requireNodeError(t, err, "db", component.PhaseClose, errClose)
	for i, e := range errs {
		if e != err { //nolint:errorlint // identity: every caller gets the first call's error value
			t.Fatalf("Stop call %d returned %v, want the first call's result", i, e)
		}
	}
	if again := app.Stop(testContext(t)); again != err { //nolint:errorlint // identity, as above
		t.Fatalf("a later Stop returned %v, want the first result", again)
	}
	events := rec.list()
	for _, e := range []string{"drain api", "stop api", "drain db", "stop db", "close db"} {
		requireOnce(t, events, e)
	}
}

func TestStopWithoutStartClosesButDoesNotStop(t *testing.T) {
	rec := &recorder{}
	app := mustNew(t, []component.Root{diamond(rec)})
	if err := app.Stop(testContext(t)); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	events := rec.list()
	for _, n := range []string{"cfg", "db", "cache", "api"} {
		requireOnce(t, events, "close "+n)
		requireAbsent(t, events, "stop "+n)
		requireAbsent(t, events, "drain "+n)
	}
	requireBefore(t, events, "close api", "close db")
	requireBefore(t, events, "close db", "close cfg")
}

func TestStopAbandonsTasksAfterTheDeadlineButStillRunsHooks(t *testing.T) {
	rec := &recorder{}
	stuck := make(chan struct{})
	t.Cleanup(func() { close(stuck) })
	db := component.ProvideValue(func() int { return 1 }, hooks[int](rec, "db")...)
	worker := component.MapValue(db, func(int) string { return "worker" },
		component.Label[string]("worker"),
		component.Go(func(string, context.Context) error { <-stuck; return nil }),
		component.OnStop(func(_ string, ctx context.Context) error {
			rec.add("stop worker; ctx done=" + boolString(ctx.Err() != nil))
			return nil
		}))
	app := mustNew(t, []component.Root{worker})
	if err := app.Start(testContext(t)); err != nil {
		t.Fatalf("Start: %v", err)
	}
	stopCtx, cancel := context.WithCancel(testContext(t))
	cancel()

	err := app.Stop(stopCtx)

	requireNodeError(t, err, "worker", component.PhaseStop, component.ErrAbandoned)
	requireIs(t, err, context.Canceled)
	events := rec.list()
	requireOnce(t, events, "stop worker; ctx done=true")
	requireBefore(t, events, "stop db", "close db")
}

func TestWaitReturnsTheFirstTaskExit(t *testing.T) {
	failure := errors.New("consumer lost")
	tests := []struct {
		name string
		task func(int, context.Context) error
		want error
	}{
		{name: "error", task: func(int, context.Context) error { return failure }, want: failure},
		{name: "nil", task: func(int, context.Context) error { return nil }, want: component.ErrExited},
		{name: "panic", task: func(int, context.Context) error { panic("task") }, want: component.ErrPanic},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			worker := component.ProvideValue(func() int { return 1 }, component.Label[int]("worker"), component.Go(tt.task))
			app := mustNew(t, []component.Root{worker})
			if err := app.Start(testContext(t)); err != nil {
				t.Fatalf("Start: %v", err)
			}

			err := app.Wait(testContext(t))
			requireNodeError(t, err, "worker", component.PhaseTask, tt.want)
			// Wait does not stop anything, and a second Wait sees the same exit.
			requireIs(t, app.Wait(testContext(t)), tt.want)
			// Stop does not report the exit Wait already returned.
			if err := app.Stop(testContext(t)); err != nil {
				t.Fatalf("Stop: %v", err)
			}
		})
	}
}

func TestWaitReturnsTheContextCauseOrErrStopped(t *testing.T) {
	block := component.ProvideValue(func() int { return 1 },
		component.Go(func(_ int, ctx context.Context) error { <-ctx.Done(); return nil }))
	app := mustNew(t, []component.Root{block})
	if err := app.Start(testContext(t)); err != nil {
		t.Fatalf("Start: %v", err)
	}

	ctx, cancel := context.WithCancelCause(testContext(t))
	cause := errors.New("SIGTERM")
	cancel(cause)
	if err := app.Wait(ctx); !errors.Is(err, cause) {
		t.Fatalf("Wait = %v, want the cancellation cause", err)
	}

	waited := make(chan error)
	go func() { waited <- app.Wait(testContext(t)) }()
	if err := app.Stop(testContext(t)); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := <-waited; !errors.Is(err, component.ErrStopped) {
		t.Fatalf("Wait during Stop = %v, want ErrStopped", err)
	}
}

func TestHookPanicsBecomeErrors(t *testing.T) {
	boom := component.ProvideValue(func() int { return 1 }, component.Label[int]("boom"),
		component.OnStart(func(int, context.Context) error { panic("start") }))
	app := mustNew(t, []component.Root{boom})
	requireNodeError(t, app.Start(testContext(t)), "boom", component.PhaseStart, component.ErrPanic)
	if err := app.Stop(testContext(t)); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestStopWaitsForARunningStart(t *testing.T) {
	rec := &recorder{}
	entered, release := make(chan struct{}), make(chan struct{})
	slow := component.ProvideValue(func() int { return 1 }, append(hooks[int](rec, "slow"),
		component.OnStart(func(int, context.Context) error { close(entered); <-release; return nil }))...)
	next := component.MapValue(slow, func(int) uint { return 2 }, hooks[uint](rec, "next")...)
	app := mustNew(t, []component.Root{next})

	started := make(chan error)
	go func() { started <- app.Start(testContext(t)) }()
	<-entered
	stopped := make(chan error)
	go func() { stopped <- app.Stop(testContext(t)) }()
	// Stop has begun once Get reports it; Start then dispatches nothing more.
	for {
		if _, err := component.Get(app, next); errors.Is(err, component.ErrStopped) {
			break
		}
	}
	close(release)

	requireIs(t, <-started, component.ErrStopped)
	if err := <-stopped; err != nil {
		t.Fatalf("Stop: %v", err)
	}
	events := rec.list()
	requireAbsent(t, events, "start next")
	requireBefore(t, events, "start slow", "stop slow")
}

func TestStopReportsTaskFailuresWaitDidNotReturn(t *testing.T) {
	errA, errB := errors.New("a failed"), errors.New("b failed")
	// Each task fails whether or not Stop has begun, so the outcome does not
	// depend on scheduling: Stop must report every error Wait did not.
	a := component.ProvideValue(func() int { return 1 }, component.Label[int]("a"),
		component.Go(func(int, context.Context) error { return errA }))
	b := component.ProvideValue(func() uint { return 2 }, component.Label[uint]("b"),
		component.Go(func(uint, context.Context) error { return errB }))

	t.Run("without Wait", func(t *testing.T) {
		app := mustNew(t, []component.Root{a, b})
		if err := app.Start(testContext(t)); err != nil {
			t.Fatalf("Start: %v", err)
		}
		err := app.Stop(testContext(t))
		requireNodeError(t, err, "a", component.PhaseTask, errA)
		requireNodeError(t, err, "b", component.PhaseTask, errB)
	})

	t.Run("after Wait", func(t *testing.T) {
		app := mustNew(t, []component.Root{a, b})
		if err := app.Start(testContext(t)); err != nil {
			t.Fatalf("Start: %v", err)
		}
		waited := app.Wait(testContext(t))
		stopped := app.Stop(testContext(t))
		reported, other := errA, errB
		if errors.Is(waited, errB) {
			reported, other = errB, errA
		}
		requireIs(t, waited, reported)
		requireIs(t, stopped, other)
		if errors.Is(stopped, reported) {
			t.Fatalf("Stop repeated the exit Wait returned: %v", stopped)
		}
		// After Stop has run, Wait reports the stop, not the exit again.
		if err := app.Wait(testContext(t)); !errors.Is(err, component.ErrStopped) && !errors.Is(err, reported) {
			t.Fatalf("Wait after Stop = %v", err)
		}
	})
}

func TestTaskGoexitIsReportedAsAnExit(t *testing.T) {
	worker := component.ProvideValue(func() int { return 1 }, component.Label[int]("worker"),
		component.Go(func(int, context.Context) error { runtime.Goexit(); return nil }))
	app := mustNew(t, []component.Root{worker})
	if err := app.Start(testContext(t)); err != nil {
		t.Fatalf("Start: %v", err)
	}
	requireNodeError(t, app.Wait(testContext(t)), "worker", component.PhaseTask, component.ErrExited)
	if err := app.Stop(testContext(t)); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestStopCancelsARunningStart(t *testing.T) {
	entered := make(chan struct{})
	slow := component.ProvideValue(func() int { return 1 }, component.Label[int]("slow"),
		component.OnStart(func(_ int, ctx context.Context) error {
			close(entered)
			<-ctx.Done() // a ping retrying until its context ends
			return context.Cause(ctx)
		}))
	app := mustNew(t, []component.Root{slow})

	started := make(chan error)
	// The start context never ends on its own; only Stop can end the hook.
	go func() { started <- app.Start(context.Background()) }()
	<-entered
	if err := app.Stop(testContext(t)); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	requireNodeError(t, <-started, "slow", component.PhaseStart, component.ErrStopped)
}
