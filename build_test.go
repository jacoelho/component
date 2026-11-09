package component_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/jacoelho/component"
)

type config struct{ dsn string }

type database struct{ dsn string }

type repository struct{ db *database }

func TestNewBuildsEachReachableNodeOnceDependenciesFirst(t *testing.T) {
	rec := &recorder{}
	cfg := component.Value(config{dsn: "postgres://primary"})
	db := component.MapValue(cfg, func(c config) *database {
		rec.add("build db")
		return &database{dsn: c.dsn}
	})
	repo := component.MapValue(db, func(d *database) *repository {
		rec.add("build repo")
		return &repository{db: d}
	})
	// Both inputs are the same node: it is built once and passed twice.
	pair := component.MapValue2(db, repo, func(d *database, r *repository) bool {
		rec.add("build pair")
		return d == r.db
	})
	unused := component.ProvideValue(func() int { rec.add("build unused"); return 1 })
	_ = unused

	app := mustNew(t, []component.Root{pair, repo, pair})

	events := rec.list()
	requireBefore(t, events, "build db", "build repo")
	requireBefore(t, events, "build repo", "build pair")
	requireAbsent(t, events, "build unused")
	same, err := component.Get(app, pair)
	if err != nil || !same {
		t.Fatalf("Get(pair) = %v, %v; want true: the shared db is one value", same, err)
	}
	got, err := component.Get(app, repo)
	if err != nil || got.db.dsn != "postgres://primary" {
		t.Fatalf("Get(repo) = %+v, %v; want the configured dsn", got, err)
	}
}

func TestInputsKeepArgumentOrderAcrossAllEightPositions(t *testing.T) {
	refs := make([]component.Ref[string], 8)
	for i, s := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		refs[i] = component.Value(s)
	}
	joined := component.MapValue8(refs[0], refs[1], component.Started(refs[2]), refs[3],
		refs[4], refs[5], component.Started(refs[6]), refs[7],
		func(a, b, c, d, e, f, g, h string) string { return a + b + c + d + e + f + g + h })
	app := mustNew(t, []component.Root{joined})
	got, err := component.Get(app, joined)
	if err != nil || got != "abcdefgh" {
		t.Fatalf("Get = %q, %v; want %q", got, err, "abcdefgh")
	}
}

func TestSameTypedNodesStayDistinct(t *testing.T) {
	first, second := component.Value(2), component.Value(3)
	diff := component.MapValue2(first, second, func(a, b int) int { return a - b })
	app := mustNew(t, []component.Root{diff})
	if got, _ := component.Get(app, diff); got != -1 {
		t.Fatalf("Get = %d, want 2-3 = -1", got)
	}
}

func TestNilInterfaceValuesFlowThrough(t *testing.T) {
	var none error
	in := component.Value(none)
	isNil := component.MapValue(in, func(err error) bool { return err == nil })
	app := mustNew(t, []component.Root{isNil})
	if got, _ := component.Get(app, isNil); !got {
		t.Fatal("a nil interface input reached the constructor as non-nil")
	}
}

func TestValueStoresFunctionsWithoutCallingThem(t *testing.T) {
	called := false
	fn := component.Value(func() { called = true })
	app := mustNew(t, []component.Root{fn})
	if called {
		t.Fatal("Value called its function")
	}
	got, err := component.Get(app, fn)
	if err != nil || got == nil {
		t.Fatalf("Get = %v; want the stored function", err)
	}
}

func TestConstructorFormsPassResultsErrorsAndContext(t *testing.T) {
	type key struct{}
	ctx := context.WithValue(testContext(t), key{}, "from New")
	fromCtx := func(ctx context.Context) string {
		if s, ok := ctx.Value(key{}).(string); ok {
			return s
		}
		return "missing"
	}
	failure := errors.New("constructor failed")
	in := component.Value("in")

	tests := []struct {
		name    string
		ref     component.Ref[string]
		want    string
		wantErr error
	}{
		{name: "ProvideValue", ref: component.ProvideValue(func() string { return "pv" }), want: "pv"},
		{name: "Provide", ref: component.Provide(func() (string, error) { return "p", nil }), want: "p"},
		{name: "Provide error", ref: component.Provide(func() (string, error) { return "", failure }), wantErr: failure},
		{name: "ProvideContext", ref: component.ProvideContext(func(ctx context.Context) (string, error) { return fromCtx(ctx), nil }), want: "from New"},
		{name: "MapValue", ref: component.MapValue(in, func(s string) string { return s + "-mv" }), want: "in-mv"},
		{name: "Map", ref: component.Map(in, func(s string) (string, error) { return s + "-m", nil }), want: "in-m"},
		{name: "Map error", ref: component.Map(in, func(string) (string, error) { return "", failure }), wantErr: failure},
		{name: "MapContext", ref: component.MapContext(in, func(ctx context.Context, s string) (string, error) { return s + "-" + fromCtx(ctx), nil }), want: "in-from New"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app, err := component.New(ctx, []component.Root{tt.ref})
			if tt.wantErr != nil {
				requireIs(t, err, tt.wantErr)
				requireNodeError(t, err, "", component.PhaseBuild, tt.wantErr)
				if err := app.Stop(ctx); err != nil {
					t.Fatalf("Stop after a failed New: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if got, _ := component.Get(app, tt.ref); got != tt.want {
				t.Fatalf("Get = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNewRejectsInvalidDeclarationsBeforeBuilding(t *testing.T) {
	built := 0
	valid := component.ProvideValue(func() int { built++; return 1 })
	var nilIn component.In[int]
	var zero component.Ref[int]

	tests := []struct {
		name  string
		roots []component.Root
		opts  []component.AppOption
		want  error
	}{
		{name: "zero root", roots: []component.Root{valid, zero}, want: component.ErrInvalidReference},
		{name: "nil root", roots: []component.Root{valid, nil}, want: component.ErrInvalidReference},
		{name: "zero input", roots: []component.Root{valid, component.MapValue(zero, func(int) int { return 0 })}, want: component.ErrInvalidReference},
		{name: "nil In input", roots: []component.Root{valid, component.MapValue(nilIn, func(int) int { return 0 })}, want: component.ErrInvalidReference},
		{name: "nil constructor", roots: []component.Root{valid, component.Provide[int](nil)}, want: component.ErrInvalidDefinition},
		{name: "nil hook", roots: []component.Root{component.ProvideValue(func() int { return 0 }, component.OnStart[int](nil)), valid}, want: component.ErrInvalidDefinition},
		{name: "nil close hook", roots: []component.Root{component.ProvideValue(func() int { return 0 }, component.OnClose[int](nil)), valid}, want: component.ErrInvalidDefinition},
		{name: "zero option", roots: []component.Root{component.ProvideValue(func() int { return 0 }, component.Option[int]{}), valid}, want: component.ErrInvalidDefinition},
		{name: "empty label", roots: []component.Root{component.ProvideValue(func() int { return 0 }, component.Label[int]("")), valid}, want: component.ErrInvalidDefinition},
		{name: "two labels", roots: []component.Root{component.ProvideValue(func() int { return 0 }, component.Label[int]("a"), component.Label[int]("b")), valid}, want: component.ErrInvalidDefinition},
		{name: "zero AppOption", roots: []component.Root{valid}, opts: []component.AppOption{{}}, want: component.ErrInvalidDefinition},
		{name: "replace zero ref", roots: []component.Root{valid}, opts: []component.AppOption{component.Replace(zero, 1)}, want: component.ErrInvalidReference},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			built = 0
			app, err := component.New(testContext(t), tt.roots, tt.opts...)
			requireIs(t, err, tt.want)
			if built != 0 {
				t.Fatalf("a constructor ran %d times before validation finished", built)
			}
			// The failed App is still safe to use: nothing was built, so
			// Stop has nothing to do.
			requireIs(t, app.Start(testContext(t)), component.ErrBuildFailed)
			if err := app.Stop(testContext(t)); err != nil {
				t.Fatalf("Stop after a rejected declaration: %v", err)
			}
		})
	}
}

func TestNewReportsEveryDeclarationError(t *testing.T) {
	a := component.Provide[int](nil, component.Label[int]("first"))
	b := component.Provide[int](nil, component.Label[int]("second"))
	_, err := component.New(testContext(t), []component.Root{a, b})
	requireNodeError(t, err, "first", component.PhaseBuild, component.ErrInvalidDefinition)
	requireNodeError(t, err, "second", component.PhaseBuild, component.ErrInvalidDefinition)
}

func TestNewNamesAnInvalidInputByItsArgumentPosition(t *testing.T) {
	var zero component.Ref[int]
	valid := component.Value(1)
	n := component.MapValue2(valid, zero, func(int, int) int { return 0 }, component.Label[int]("sum"))
	_, err := component.New(testContext(t), []component.Root{n})
	requireNodeError(t, err, "sum", component.PhaseBuild, component.ErrInvalidReference)
	// Zero-based, like the root index in the same family of errors.
	if !strings.Contains(err.Error(), "input 1 ") {
		t.Fatalf("error does not name input 1, the second argument: %v", err)
	}
}

func TestNewReportsEveryInvalidOptionOfOneNode(t *testing.T) {
	n := component.ProvideValue(func() int { return 0 },
		component.OnStart[int](nil), component.OnDrain[int](nil), component.Label[int](""))
	_, err := component.New(testContext(t), []component.Root{n})
	requireIs(t, err, component.ErrInvalidDefinition)
	// The message must name each option to fix, not only the last one.
	for _, option := range []string{"OnStart", "OnDrain", "Label"} {
		if !strings.Contains(err.Error(), option+" requires") {
			t.Errorf("error does not report the invalid %s option: %v", option, err)
		}
	}
}

func TestNewFailureLeavesTeardownToStop(t *testing.T) {
	rec := &recorder{}
	failure := errors.New("dial failed")
	pool := component.ProvideValue(func() string { rec.add("build pool"); return "pool" }, hooks[string](rec, "pool")...)
	// cache is started during New because migrate needs it running.
	cache := component.ProvideValue(func() int { rec.add("build cache"); return 1 }, hooks[int](rec, "cache")...)
	migrate := component.MapValue2(pool, component.Started(cache), func(string, int) bool { rec.add("build migrate"); return true },
		hooks[bool](rec, "migrate")...)
	broken := component.Map(migrate, func(bool) (float64, error) { return 0, failure }, component.Label[float64]("broken"))
	after := component.MapValue(broken, func(float64) uint { rec.add("build after"); return 0 })

	app, err := component.New(testContext(t), []component.Root{after})

	requireNodeError(t, err, "broken", component.PhaseBuild, failure)
	// The failed App refuses everything but Stop, and New tore nothing down.
	requireIs(t, app.Start(testContext(t)), component.ErrBuildFailed)
	requireIs(t, app.Wait(testContext(t)), component.ErrBuildFailed)
	if _, err := component.Get(app, pool); !errors.Is(err, component.ErrBuildFailed) {
		t.Fatalf("Get on a failed App = %v, want ErrBuildFailed", err)
	}
	requireAbsent(t, rec.list(), "close pool")
	requireAbsent(t, rec.list(), "stop cache")

	if err := app.Stop(testContext(t)); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, err := component.Get(app, pool); !errors.Is(err, component.ErrStopped) {
		t.Fatalf("Get after Stop = %v, want ErrStopped", err)
	}
	events := rec.list()
	requireAbsent(t, events, "build after")
	// Built but not started: closed, never stopped or drained.
	requireOnce(t, events, "close pool")
	requireAbsent(t, events, "stop pool")
	requireAbsent(t, events, "drain pool")
	requireOnce(t, events, "close migrate")
	requireAbsent(t, events, "stop migrate")
	// Started early: drained, stopped and closed, after its dependent.
	requireBefore(t, events, "start cache", "build migrate")
	requireBefore(t, events, "drain cache", "close migrate")
	requireBefore(t, events, "close migrate", "stop cache")
	requireBefore(t, events, "stop cache", "close cache")
}

// A build cancelled by its own context must still shut down gracefully: the
// stop context, not the dead build context, governs teardown.
func TestStopAfterACancelledNewUsesTheStopContext(t *testing.T) {
	ctx, cancel := context.WithCancel(testContext(t))
	var stopCtxErr error
	finished := make(chan struct{})
	worker := component.ProvideValue(func() string { return "worker" }, component.Label[string]("worker"),
		component.Go(func(_ string, ctx context.Context) error {
			defer close(finished)
			<-ctx.Done()
			return ctx.Err()
		}),
		component.OnStop(func(_ string, ctx context.Context) error {
			stopCtxErr = ctx.Err()
			return nil
		}))
	consumer := component.MapContext(component.Started(worker), func(ctx context.Context, _ string) (int, error) {
		cancel() // the signal or startup deadline arrives mid-build
		return 0, ctx.Err()
	}, component.Label[int]("consumer"))

	app, err := component.New(ctx, []component.Root{consumer})
	requireNodeError(t, err, "consumer", component.PhaseBuild, context.Canceled)

	if err := app.Stop(testContext(t)); err != nil {
		t.Fatalf("Stop = %v, want nil: the task ended cleanly within the stop context", err)
	}
	if stopCtxErr != nil {
		t.Fatalf("OnStop saw a context that was already done: %v", stopCtxErr)
	}
	select {
	case <-finished:
	default:
		t.Fatal("Stop returned before the started task finished")
	}
}

// A task started during New may exit while a later constructor is still
// running. The failed App must record it without racing New, and Stop must
// report it.
func TestStopReportsATaskThatExitedDuringAFailedNew(t *testing.T) {
	failure := errors.New("dial failed")
	returning := make(chan struct{})
	worker := component.ProvideValue(func() string { return "worker" }, component.Label[string]("worker"),
		component.Go(func(string, context.Context) error {
			close(returning)
			return nil
		}))
	consumer := component.Map(component.Started(worker), func(string) (int, error) {
		<-returning
		return 0, failure
	}, component.Label[int]("consumer"))

	app, err := component.New(testContext(t), []component.Root{consumer})
	requireNodeError(t, err, "consumer", component.PhaseBuild, failure)

	requireNodeError(t, app.Stop(testContext(t)), "worker", component.PhaseTask, component.ErrExited)
}

func TestNewRecoversConstructorPanics(t *testing.T) {
	rec := &recorder{}
	dep := component.ProvideValue(func() int { return 1 }, hooks[int](rec, "dep")...)
	boom := component.MapValue(dep, func(int) string { panic("boom") }, component.Label[string]("boom"))

	app, err := component.New(testContext(t), []component.Root{boom})

	requireNodeError(t, err, "boom", component.PhaseBuild, component.ErrPanic)
	var pe *component.PanicError
	if !errors.As(err, &pe) || pe.Value != "boom" || len(pe.Stack) == 0 {
		t.Fatalf("PanicError = %+v, want value %q with a stack", pe, "boom")
	}
	if err := app.Stop(testContext(t)); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	requireOnce(t, rec.list(), "close dep")
}

func TestPanicErrorKeepsAnErrorValueInspectable(t *testing.T) {
	cause := errors.New("cause")
	boom := component.ProvideValue(func() int { panic(cause) })
	_, err := component.New(testContext(t), []component.Root{boom})
	requireIs(t, err, component.ErrPanic)
	requireIs(t, err, cause)
}

func TestStartedInputIsRunningBeforeTheConsumerIsBuilt(t *testing.T) {
	rec := &recorder{}
	cfg := component.ProvideValue(func() int { return 1 }, hooks[int](rec, "cfg")...)
	db := component.MapValue(cfg, func(int) string { rec.add("build db"); return "db" }, hooks[string](rec, "db")...)
	unrelated := component.ProvideValue(func() uint { return 1 }, hooks[uint](rec, "unrelated")...)
	repo := component.MapValue2(component.Started(db), unrelated, func(string, uint) bool { rec.add("build repo"); return true },
		hooks[bool](rec, "repo")...)

	app := mustNew(t, []component.Root{repo})
	events := rec.list()
	requireBefore(t, events, "start cfg", "start db")
	requireBefore(t, events, "start db", "build repo")
	requireAbsent(t, events, "start unrelated")

	if err := app.Start(testContext(t)); err != nil {
		t.Fatalf("Start: %v", err)
	}
	events = rec.list()
	requireOnce(t, events, "start db") // Start does not start it again
	requireBefore(t, events, "start unrelated", "start repo")
	if err := app.Stop(testContext(t)); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	events = rec.list()
	requireBefore(t, events, "stop repo", "stop db")
	requireBefore(t, events, "stop db", "stop cfg")
}

func TestReplaceUsesTheValueAndSkipsTheReplacedSubgraph(t *testing.T) {
	rec := &recorder{}
	dsn := component.ProvideValue(func() string { rec.add("build dsn"); return "postgres://real" })
	db := component.MapValue(dsn, func(s string) *database { rec.add("build db"); return &database{dsn: s} },
		hooks[*database](rec, "db")...)
	repo := component.MapValue(db, func(d *database) *repository { return &repository{db: d} })
	fake := &database{dsn: "memory"}

	app := mustNew(t, []component.Root{repo}, component.Replace(db, fake))
	if err := app.Start(testContext(t)); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := app.Stop(testContext(t)); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	if events := rec.list(); len(events) != 0 {
		t.Fatalf("replaced node or its inputs ran: %q", events)
	}
	if got, _ := component.Get(mustNew(t, []component.Root{repo}, component.Replace(db, fake)), repo); got.db != fake {
		t.Fatalf("repo received %+v, want the replacement", got.db)
	}
	if _, err := component.Get(app, dsn); !errors.Is(err, component.ErrStopped) {
		t.Fatalf("Get after Stop = %v, want ErrStopped", err)
	}
}

func TestReplaceRejectsUnreachableAndDuplicateRefs(t *testing.T) {
	root := component.ProvideValue(func() int { return 1 })
	other := component.ProvideValue(func() int { return 2 })

	_, err := component.New(testContext(t), []component.Root{root}, component.Replace(other, 3))
	requireIs(t, err, component.ErrInvalidReference)

	_, err = component.New(testContext(t), []component.Root{root}, component.Replace(root, 3), component.Replace(root, 4))
	requireIs(t, err, component.ErrInvalidReference)
}

func TestAppsFromOneGraphAreIndependent(t *testing.T) {
	builds := 0
	counter := component.ProvideValue(func() *int { builds++; v := builds; return &v })
	first := mustNew(t, []component.Root{counter})
	second := mustNew(t, []component.Root{counter})
	a, _ := component.Get(first, counter)
	b, _ := component.Get(second, counter)
	if a == b || *a != 1 || *b != 2 {
		t.Fatalf("values = %d, %d; want two separately built values 1 and 2", *a, *b)
	}
}

func TestGetRejectsRefsOutsideTheApp(t *testing.T) {
	in := component.Value(1)
	out := component.Value(2)
	app := mustNew(t, []component.Root{in})
	if _, err := component.Get(app, out); !errors.Is(err, component.ErrInvalidReference) {
		t.Fatalf("Get(foreign) = %v, want ErrInvalidReference", err)
	}
	var zero component.Ref[int]
	if _, err := component.Get(app, zero); !errors.Is(err, component.ErrInvalidReference) {
		t.Fatalf("Get(zero) = %v, want ErrInvalidReference", err)
	}
}

func newPrimaryDB(config) *database { return &database{} }

func TestPlanListsLabelsAndEdgesInBuildOrder(t *testing.T) {
	cfg := component.Value(config{})
	db := component.MapValue(cfg, newPrimaryDB)
	repo := component.MapValue(db, func(d *database) *repository { return &repository{db: d} }, component.Label[*repository]("repo"))
	app := mustNew(t, []component.Root{repo})

	plan := app.Plan()
	labels := make([]string, len(plan))
	for i, n := range plan {
		labels[i] = n.Label
	}
	want := []string{"Value[component_test.config]", "component_test.newPrimaryDB", "repo"}
	if !slices.Equal(labels, want) {
		t.Fatalf("labels = %q, want %q", labels, want)
	}
	if !slices.Equal(plan[1].Deps, []int{0}) || !slices.Equal(plan[2].Deps, []int{1}) || len(plan[0].Deps) != 0 {
		t.Fatalf("deps = %v, %v, %v; want [], [0], [1]", plan[0].Deps, plan[1].Deps, plan[2].Deps)
	}
	plan[2].Deps[0] = 99
	if app.Plan()[2].Deps[0] != 1 {
		t.Fatal("Plan returned the App's own slice")
	}
}

func TestErrorsNameTheNodeAndPhase(t *testing.T) {
	err := &component.NodeError{Label: "storage.open", Phase: component.PhaseBuild, Err: errors.New("refused")}
	if got := err.Error(); !strings.Contains(got, "storage.open") || !strings.Contains(got, "build") || !strings.Contains(got, "refused") {
		t.Fatalf("Error() = %q, want the label, phase and cause", got)
	}
}

func TestLongChainsBuildStartAndStop(t *testing.T) {
	const depth = 100_000
	ref := component.Value(0)
	for range depth {
		ref = component.MapValue(ref, func(n int) int { return n + 1 })
	}
	app := mustNew(t, []component.Root{ref})
	if err := app.Start(testContext(t)); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got, _ := component.Get(app, ref); got != depth {
		t.Fatalf("Get = %d, want %d", got, depth)
	}
	if err := app.Stop(testContext(t)); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}
