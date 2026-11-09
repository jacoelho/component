package component_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jacoelho/component"
)

// recorder collects events from constructors, hooks and tasks, which may run
// on different goroutines.
type recorder struct {
	mu     sync.Mutex
	events []string
}

func (r *recorder) add(event string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

func (r *recorder) list() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.events)
}

// before asserts that a and b each happened exactly once, a first. Unrelated
// nodes have no contractual order, so tests assert only the pairs the graph
// orders.
func requireBefore(t *testing.T, events []string, a, b string) {
	t.Helper()
	requireOnce(t, events, a)
	requireOnce(t, events, b)
	if slices.Index(events, a) > slices.Index(events, b) {
		t.Fatalf("%q happened after %q; events: %q", a, b, events)
	}
}

func requireOnce(t *testing.T, events []string, event string) {
	t.Helper()
	n := 0
	for _, e := range events {
		if e == event {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%q happened %d times, want once; events: %q", event, n, events)
	}
}

func requireAbsent(t *testing.T, events []string, event string) {
	t.Helper()
	if slices.Contains(events, event) {
		t.Fatalf("%q happened; events: %q", event, events)
	}
}

func requireIs(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("error %v does not match %v", err, target)
	}
}

// requireNodeError asserts that err contains a *NodeError for label and
// phase whose cause matches target. An empty label matches any node.
func requireNodeError(t *testing.T, err error, label, phase string, target error) {
	t.Helper()
	for _, e := range flatten(err) {
		var ne *component.NodeError
		if errors.As(e, &ne) && (label == "" || ne.Label == label) && ne.Phase == phase && errors.Is(ne, target) {
			return
		}
	}
	t.Fatalf("error %v has no NodeError{Label: %q, Phase: %q} matching %v", err, label, phase, target)
}

// flatten splits joined errors, so each NodeError in a join can be checked.
func flatten(err error) []error {
	if err == nil {
		return nil
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		var out []error
		for _, e := range joined.Unwrap() {
			out = append(out, flatten(e)...)
		}
		return out
	}
	return []error{err}
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func mustNew(t *testing.T, roots []component.Root, opts ...component.AppOption) *component.App {
	t.Helper()
	app, err := component.New(testContext(t), roots, opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return app
}

// hooks returns options that record the lifecycle of a node named name.
// Teardown hooks run in reverse option order, so OnClose is declared before
// OnStop to run after it.
func hooks[T any](rec *recorder, name string) []component.Option[T] {
	return []component.Option[T]{
		component.Label[T](name),
		component.OnStart(func(T, context.Context) error { rec.add("start " + name); return nil }),
		component.OnDrain(func(T, context.Context) error { rec.add("drain " + name); return nil }),
		component.OnClose(func(T) error { rec.add("close " + name); return nil }),
		component.OnStop(func(T, context.Context) error { rec.add("stop " + name); return nil }),
	}
}
