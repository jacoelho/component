package component

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"slices"
	"sync"
)

// App owns one value per node reachable from its roots, and the hooks and
// tasks attached to them. New builds every value, Start starts them in
// dependency order, and Stop stops them in reverse. An App is single-use.
type App struct {
	stopErr   error
	index     map[*node]int
	stopBegun chan struct{} // closed by the transition to appStopping
	exited    chan struct{}
	exitTask  *task                   // guarded by mu
	stopStart context.CancelCauseFunc // guarded by mu; set only while appStarting
	nodes     []*instance
	// op serialises Start and Stop's teardown, so Stop waits for a running
	// Start to return.
	op sync.Mutex
	// mu guards state, stopStart, exitTask and exitClaim.
	mu       sync.Mutex
	stopOnce sync.Once
	// state changes only in beginStart, endStart and beginStop.
	state appState
	// exitClaim records whether Wait or Stop reported the first task exit,
	// so exactly one of them returns it.
	exitClaim exitClaim
}

// appState is the App's lifecycle. It only advances:
//
//	appBuilt -> appStarting -> appStarted
//	any of them -> appStopping
type appState uint8

const (
	appBuilt    appState = iota // New built every node; Start has not been called
	appStarting                 // Start is dispatching nodes; stopStart cancels it
	appStarted                  // Start returned, with or without an error
	appStopping                 // Stop began; final
)

type exitClaim uint8

const (
	unclaimed exitClaim = iota
	claimedByWait
	claimedByStop
)

// instance is a node's state within one App.
type instance struct {
	value  any
	decl   *node
	cancel context.CancelFunc
	label  string
	deps   []int
	hooks  []hook
	tasks  []*task
	state  nodeState
}

// nodeState is how far a node has come within one App. It only advances, and
// it decides which teardown hooks Stop owes the node.
type nodeState uint8

const (
	nodePending nodeState = iota // no value and no hooks; Stop owes nothing
	nodeBuilt                    // has a value; Stop owes OnClose
	nodeBegun                    // start began, even if it failed; Stop also owes OnDrain and OnStop
)

type task struct {
	done chan struct{}
	err  error // set before done closes
}

// AppOption configures New.
type AppOption struct {
	node  *node
	value any
	err   error
}

// Replace makes New use v for r instead of calling r's constructor. r's
// hooks never run, and nodes reachable only through r are not built. It is
// meant for tests. r must be reachable from the roots.
func Replace[T any](r Ref[T], v T) AppOption {
	if r.node == nil {
		return AppOption{err: fmt.Errorf("%w: Replace of a zero Ref", ErrInvalidReference)}
	}
	return AppOption{node: r.node, value: v}
}

// PlanNode describes one node of an App. Deps are indices into the slice
// returned by Plan.
type PlanNode struct {
	Label string
	Deps  []int
}

// New builds every node reachable from roots, each once, dependencies first.
// It rejects zero refs and invalid declarations before any constructor runs.
// A Started input starts that dependency, and everything it depends on,
// before the consumer is built.
//
// If a constructor fails or panics, New stops building, runs the Stop
// procedure with ctx on everything built so far, and returns a nil App with
// every error joined. Constructors given a context receive ctx.
func New(ctx context.Context, roots []Root, opts ...AppOption) (*App, error) {
	replace, errs := replacements(opts)
	order, index, planErrs := sortGraph(roots, replace)
	errs = append(errs, planErrs...)
	for n := range replace {
		if _, ok := index[n]; !ok {
			errs = append(errs, fmt.Errorf("%w: Replace of %s, which is not reachable from the roots", ErrInvalidReference, n.label))
		}
	}
	if len(errs) != 0 {
		return nil, errors.Join(errs...)
	}

	a := &App{
		nodes:     make([]*instance, len(order)),
		index:     index,
		stopBegun: make(chan struct{}),
		exited:    make(chan struct{}),
	}
	for i, n := range order {
		inst := &instance{decl: n, label: n.label}
		if v, ok := replace[n]; ok {
			// A replaced node is born built, with no inputs and no hooks.
			inst.value, inst.state = v, nodeBuilt
		} else {
			for _, e := range n.inputs {
				inst.deps = append(inst.deps, index[e.node])
			}
		}
		a.nodes[i] = inst
	}

	for i := range a.nodes {
		if err := a.build(ctx, i); err != nil {
			a.beginStop()
			return nil, errors.Join(err, a.teardown(ctx))
		}
	}
	return a, nil
}

func replacements(opts []AppOption) (map[*node]any, []error) {
	replace := make(map[*node]any, len(opts))
	var errs []error
	for _, o := range opts {
		switch {
		case o.err != nil:
			errs = append(errs, o.err)
		case o.node == nil:
			errs = append(errs, fmt.Errorf("%w: zero AppOption", ErrInvalidDefinition))
		default:
			if _, dup := replace[o.node]; dup {
				errs = append(errs, fmt.Errorf("%w: %s is replaced twice", ErrInvalidReference, o.node.label))
			}
			replace[o.node] = o.value
		}
	}
	return replace, errs
}

// sortGraph orders the nodes reachable from roots so that each follows its
// inputs, using an iterative depth-first walk. Refs are created only from
// existing refs, so the graph is acyclic by construction.
func sortGraph(roots []Root, replace map[*node]any) ([]*node, map[*node]int, []error) {
	type frame struct {
		n    *node
		next int
	}
	index := make(map[*node]int)
	var order []*node
	var errs []error
	for i, root := range roots {
		r := rootOf(root)
		if r == nil {
			errs = append(errs, fmt.Errorf("%w: root %d is nil or a zero Ref", ErrInvalidReference, i))
			continue
		}
		if _, done := index[r]; done {
			continue
		}
		stack := []frame{{n: r}}
		for len(stack) != 0 {
			top := &stack[len(stack)-1]
			n := top.n
			if _, replaced := replace[n]; !replaced && top.next < len(n.inputs) {
				in := n.inputs[top.next].node
				top.next++
				if in == nil {
					errs = append(errs, &NodeError{Label: n.label, Phase: PhaseBuild,
						Err: fmt.Errorf("%w: input %d is nil or a zero Ref", ErrInvalidReference, top.next)})
					continue
				}
				if _, done := index[in]; !done {
					stack = append(stack, frame{n: in})
				}
				continue
			}
			stack = stack[:len(stack)-1]
			if n.err != nil {
				errs = append(errs, &NodeError{Label: n.label, Phase: PhaseBuild, Err: n.err})
			}
			index[n] = len(order)
			order = append(order, n)
		}
	}
	return order, index, errs
}

func (a *App) build(ctx context.Context, i int) error {
	inst := a.nodes[i]
	if inst.state != nodePending {
		return nil // replaced
	}
	for j, e := range inst.decl.inputs {
		if e.started {
			if err := a.startClosure(ctx, inst.deps[j]); err != nil {
				return err
			}
		}
	}
	args := make([]any, len(inst.deps))
	for j, d := range inst.deps {
		args[j] = a.nodes[d].value
	}
	v, err := buildValue(ctx, inst.decl, args)
	if err != nil {
		return &NodeError{Label: inst.label, Phase: PhaseBuild, Err: err}
	}
	inst.value, inst.state = v, nodeBuilt
	for _, produce := range inst.decl.options {
		inst.hooks = append(inst.hooks, produce()...)
	}
	return nil
}

func buildValue(ctx context.Context, n *node, args []any) (v any, err error) {
	defer func() {
		if r := recover(); r != nil {
			v, err = nil, &PanicError{Value: r, Stack: debug.Stack()}
		}
	}()
	return n.build(ctx, args)
}

// startClosure starts node i and every node it depends on that has not
// started, in dependency order. A started node's dependencies have all
// started, so the walk stops there and the total work stays linear.
func (a *App) startClosure(ctx context.Context, i int) error {
	var pending []int
	seen := map[int]bool{i: true}
	stack := []int{i}
	for len(stack) != 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if a.nodes[n].state == nodeBegun {
			continue
		}
		pending = append(pending, n)
		for _, d := range a.nodes[n].deps {
			if !seen[d] {
				seen[d] = true
				stack = append(stack, d)
			}
		}
	}
	// Build order is a topological order, so ascending indices start
	// dependencies first.
	slices.Sort(pending)
	for _, n := range pending {
		if err := a.startNode(ctx, n); err != nil {
			return err
		}
	}
	return nil
}

// Start runs, one node at a time in dependency order, each node's OnStart
// hooks in option order and then launches its Go tasks. Each task's context
// keeps ctx's values but not its cancellation; Stop cancels it.
//
// Start stops at the first failure, when ctx is done, or when Stop is
// called, and returns an error naming the node. It does not roll back: call Stop. Start runs once;
// a second call returns ErrStarted, and a call after Stop returns ErrStopped.
func (a *App) Start(ctx context.Context) error {
	a.op.Lock()
	defer a.op.Unlock()
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	if err := a.beginStart(cancel); err != nil {
		return err
	}
	defer a.endStart()
	for i, inst := range a.nodes {
		if inst.state == nodeBegun {
			continue
		}
		// A Stop issued since beginStart has cancelled ctx with ErrStopped.
		if ctx.Err() != nil {
			return &NodeError{Label: inst.label, Phase: PhaseStart, Err: context.Cause(ctx)}
		}
		if err := a.startNode(ctx, i); err != nil {
			return err
		}
	}
	return nil
}

// beginStart moves appBuilt to appStarting and registers cancel in the same
// step, so a Stop either precedes the start and rejects it, or follows it and
// cancels it.
func (a *App) beginStart(cancel context.CancelCauseFunc) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch a.state {
	case appBuilt:
		a.state, a.stopStart = appStarting, cancel
		return nil
	case appStopping:
		return ErrStopped
	default:
		return ErrStarted
	}
}

// endStart moves appStarting to appStarted, unless Stop began meanwhile.
func (a *App) endStart() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.stopStart = nil
	if a.state == appStarting {
		a.state = appStarted
	}
}

func (a *App) startNode(ctx context.Context, i int) error {
	inst := a.nodes[i]
	inst.state = nodeBegun
	for _, h := range inst.hooks {
		if h.kind != hookStart {
			continue
		}
		if err := call(ctx, h.run, inst.value); err != nil {
			return &NodeError{Label: inst.label, Phase: PhaseStart, Err: err}
		}
	}
	if !slices.ContainsFunc(inst.hooks, func(h hook) bool { return h.kind == hookTask }) {
		return nil
	}
	taskCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	inst.cancel = cancel
	for _, h := range inst.hooks {
		if h.kind != hookTask {
			continue
		}
		t := &task{done: make(chan struct{})}
		inst.tasks = append(inst.tasks, t)
		go a.runTask(taskCtx, inst.label, h.run, inst.value, t)
	}
	return nil
}

func (a *App) runTask(ctx context.Context, label string, run func(context.Context, any) error, v any, t *task) {
	defer close(t.done)
	returned := false
	defer func() {
		if !returned {
			// runtime.Goexit skipped the code below; record the exit anyway.
			a.recordExit(ctx, label, t, errGoexit)
		}
	}()
	err := call(ctx, run, v)
	returned = true
	a.recordExit(ctx, label, t, err)
}

// errGoexit reports a task that ended through runtime.Goexit.
var errGoexit = fmt.Errorf("%w: runtime.Goexit", ErrExited)

func (a *App) recordExit(ctx context.Context, label string, t *task, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state != appStopping {
		if err == nil {
			err = ErrExited
		}
		t.err = &NodeError{Label: label, Phase: PhaseTask, Err: err}
		if a.exitTask == nil {
			a.exitTask = t
			close(a.exited)
		}
		return
	}
	if err != nil && (ctx.Err() == nil || !errors.Is(err, context.Canceled)) {
		t.err = &NodeError{Label: label, Phase: PhaseTask, Err: err}
	}
}

// Wait blocks until ctx is done, returning context.Cause(ctx), or until a
// Go task returns before Stop began, returning that task's error wrapped in
// a *NodeError, or ErrExited if it returned nil or called runtime.Goexit.
// It returns ErrStopped once Stop begins, unless the exit happened earlier
// and Stop has not reported it. Each exit is returned by Wait or by Stop,
// never both. Wait stops nothing.
func (a *App) Wait(ctx context.Context) error {
	select {
	case <-a.exited:
		return a.reportExit()
	default:
	}
	select {
	case <-a.exited:
		return a.reportExit()
	case <-a.stopBegun:
		return ErrStopped
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (a *App) reportExit() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.exitClaim == claimedByStop {
		return ErrStopped
	}
	a.exitClaim = claimedByWait
	return a.exitTask.err
}

// Stop shuts the App down:
//
//  1. OnDrain hooks of every node whose start ran, in reverse dependency
//     order.
//  2. Per node, in reverse dependency order: cancel its tasks' context, run
//     its OnStop hooks (only if its start ran) and OnClose hooks in reverse
//     option order, then wait for its tasks.
//
// Stop continues after errors and runs each hook at most once; errors are
// returned joined, each wrapped in a *NodeError. Once ctx is done, Stop no
// longer waits for tasks (each still running is reported as ErrAbandoned)
// but still runs the remaining hooks with ctx, and joins ctx.Err() to the
// result. Concurrent and later calls wait for the first and return its
// result.
//
// A Stop issued while Start runs cancels Start's context with ErrStopped and
// waits for the running hook to return; a hook that ignores its context
// delays Stop past ctx's deadline. Do not call Stop or Start from a hook or a
// task of the same App: it deadlocks. A task that wants the App to stop
// returns, and Wait reports it.
func (a *App) Stop(ctx context.Context) error {
	a.stopOnce.Do(func() {
		a.beginStop()
		a.op.Lock()
		defer a.op.Unlock()
		a.stopErr = a.teardown(ctx)
	})
	return a.stopErr
}

// beginStop moves any state to appStopping, cancelling a running Start. Each
// App reaches it once: through Stop's sync.Once, or in New before the App is
// returned.
func (a *App) beginStop() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state == appStarting {
		a.stopStart(ErrStopped)
		a.stopStart = nil
	}
	a.state = appStopping
	close(a.stopBegun)
}

func (a *App) isStopping() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state == appStopping
}

func (a *App) teardown(ctx context.Context) error {
	var errs []error
	for _, inst := range slices.Backward(a.nodes) {
		if inst.state != nodeBegun {
			continue
		}
		for _, h := range slices.Backward(inst.hooks) {
			if h.kind == hookDrain {
				if err := call(ctx, h.run, inst.value); err != nil {
					errs = append(errs, &NodeError{Label: inst.label, Phase: PhaseDrain, Err: err})
				}
			}
		}
	}
	abandoned := false
	for _, inst := range slices.Backward(a.nodes) {
		if inst.state == nodePending {
			continue
		}
		if inst.cancel != nil {
			inst.cancel()
		}
		for _, h := range slices.Backward(inst.hooks) {
			if h.kind == hookClose || (h.kind == hookStop && inst.state == nodeBegun) {
				if err := call(ctx, h.run, inst.value); err != nil {
					errs = append(errs, &NodeError{Label: inst.label, Phase: PhaseStop, Err: err})
				}
			}
		}
		for _, t := range inst.tasks {
			if !awaitTask(ctx, t) {
				abandoned = true
				errs = append(errs, &NodeError{Label: inst.label, Phase: PhaseStop, Err: ErrAbandoned})
				continue
			}
			if t.err != nil && a.claimForStop(t) {
				errs = append(errs, t.err)
			}
		}
	}
	if abandoned {
		errs = append(errs, ctx.Err())
	}
	return errors.Join(errs...)
}

// awaitTask reports whether t finished before ctx ended. A finished task is
// collected even when ctx has already ended.
func awaitTask(ctx context.Context, t *task) bool {
	select {
	case <-t.done:
		return true
	default:
	}
	select {
	case <-t.done:
		return true
	case <-ctx.Done():
		return false
	}
}

// claimForStop reports whether Stop should return t's error: always, unless
// t is the first exit and Wait has already returned it.
func (a *App) claimForStop(t *task) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.exitTask != t {
		return true
	}
	if a.exitClaim == claimedByWait {
		return false
	}
	a.exitClaim = claimedByStop
	return true
}

// Plan lists the App's nodes in start order. Stop runs them in reverse.
func (a *App) Plan() []PlanNode {
	plan := make([]PlanNode, len(a.nodes))
	for i, inst := range a.nodes {
		plan[i] = PlanNode{Label: inst.label, Deps: slices.Clone(inst.deps)}
	}
	return plan
}

// Get returns r's value. It works from a successful New until Stop begins,
// then returns ErrStopped. A Ref not in the App returns ErrInvalidReference.
func Get[T any](a *App, r Ref[T]) (T, error) {
	var zero T
	if a.isStopping() {
		return zero, ErrStopped
	}
	i, ok := a.index[r.node]
	if !ok {
		return zero, ErrInvalidReference
	}
	return as[T](a.nodes[i].value), nil
}

func call(ctx context.Context, f func(context.Context, any) error, v any) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = &PanicError{Value: r, Stack: debug.Stack()}
		}
	}()
	return f(ctx, v)
}
