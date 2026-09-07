package component_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	component "github.com/jacoelho/component"
)

func TestMappingFailurePreservesErrorAndOwnership(t *testing.T) {
	cases := []struct {
		name  string
		build func(component.Ref[*testResult], *testResult, error) component.Ref[*testResult]
	}{
		{"Map", func(input component.Ref[*testResult], value *testResult, err error) component.Ref[*testResult] {
			return component.Map(input, func(*testResult) (*testResult, error) { return value, err }, component.Managed[*testResult]())
		}},
		{"Map2", func(input component.Ref[*testResult], value *testResult, err error) component.Ref[*testResult] {
			return component.Map2(input, component.Value(3), func(*testResult, int) (*testResult, error) { return value, err }, component.Managed[*testResult]())
		}},
		{"Map3", func(input component.Ref[*testResult], value *testResult, err error) component.Ref[*testResult] {
			return component.Map3(input, component.Value(3), component.Value(5), func(*testResult, int, int) (*testResult, error) { return value, err }, component.Managed[*testResult]())
		}},
		{"Map4", func(input component.Ref[*testResult], value *testResult, err error) component.Ref[*testResult] {
			return component.Map4(input, component.Value(3), component.Value(5), component.Value(7), func(*testResult, int, int, int) (*testResult, error) { return value, err }, component.Managed[*testResult]())
		}},
		{"MapContext", func(input component.Ref[*testResult], value *testResult, err error) component.Ref[*testResult] {
			return component.MapContext(input, func(context.Context, *testResult) (*testResult, error) { return value, err }, component.Managed[*testResult]())
		}},
		{"MapContext2", func(input component.Ref[*testResult], value *testResult, err error) component.Ref[*testResult] {
			return component.MapContext2(input, component.Value(3), func(context.Context, *testResult, int) (*testResult, error) { return value, err }, component.Managed[*testResult]())
		}},
		{"MapContext3", func(input component.Ref[*testResult], value *testResult, err error) component.Ref[*testResult] {
			return component.MapContext3(input, component.Value(3), component.Value(5), func(context.Context, *testResult, int, int) (*testResult, error) { return value, err }, component.Managed[*testResult]())
		}},
		{"MapContext4", func(input component.Ref[*testResult], value *testResult, err error) component.Ref[*testResult] {
			return component.MapContext4(input, component.Value(3), component.Value(5), component.Value(7), func(context.Context, *testResult, int, int, int) (*testResult, error) { return value, err }, component.Managed[*testResult]())
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			failure := errors.New("mapping construction failed")
			dependency, rejected := &testResult{}, &testResult{}
			input := component.ProvideValue(func() *testResult { return dependency }, component.Managed[*testResult]())
			failed := tc.build(input, rejected, failure)
			var dependentCalls atomic.Int32
			dependent := component.MapValue(failed, func(*testResult) int {
				dependentCalls.Add(1)
				return 42
			})
			rt := newTestRuntime(t, dependent)
			if err := rt.Start(t.Context()); !errors.Is(err, failure) {
				t.Errorf("Start error = %v, want original constructor error %v", err, failure)
			}
			if got := dependentCalls.Load(); got != 0 {
				t.Errorf("dependent constructor calls = %d, want 0 after dependency failure", got)
			}
			if starts, stops := dependency.starts.Load(), dependency.stops.Load(); starts != 1 || stops != 0 {
				t.Errorf("dependency hooks before caller cleanup = (%d starts, %d stops), want (1, 0)", starts, stops)
			}
			if err := rt.Stop(t.Context()); err != nil {
				t.Fatalf("Stop after constructor failure: %v", err)
			}
			if got := dependency.stops.Load(); got != 1 {
				t.Errorf("dependency Stop calls = %d, want 1", got)
			}
			if starts, stops := rejected.starts.Load(), rejected.stops.Load(); starts != 0 || stops != 0 {
				t.Errorf("rejected value hooks = (%d starts, %d stops), want (0, 0): constructor error must prevent ownership transfer", starts, stops)
			}
		})
	}
}

func TestMappingReceivesExactStartupContext(t *testing.T) {
	cases := []struct {
		name  string
		build func(func(context.Context) (int, error)) component.Ref[int]
	}{
		{"MapContext", func(create func(context.Context) (int, error)) component.Ref[int] {
			return component.MapContext(component.Value(2), func(ctx context.Context, _ int) (int, error) { return create(ctx) })
		}},
		{"MapContext2", func(create func(context.Context) (int, error)) component.Ref[int] {
			return component.MapContext2(component.Value(2), component.Value(3), func(ctx context.Context, _, _ int) (int, error) { return create(ctx) })
		}},
		{"MapContext3", func(create func(context.Context) (int, error)) component.Ref[int] {
			return component.MapContext3(component.Value(2), component.Value(3), component.Value(5), func(ctx context.Context, _, _, _ int) (int, error) { return create(ctx) })
		}},
		{"MapContext4", func(create func(context.Context) (int, error)) component.Ref[int] {
			return component.MapContext4(component.Value(2), component.Value(3), component.Value(5), component.Value(7), func(ctx context.Context, _, _, _, _ int) (int, error) { return create(ctx) })
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			startup := &exactContext{Context: base}
			var received context.Context
			ref := tc.build(func(ctx context.Context) (int, error) {
				received = ctx
				return 42, nil
			})
			rt := newTestRuntime(t, ref)
			t.Cleanup(func() {
				if err := rt.Stop(context.Background()); err != nil {
					t.Errorf("Stop: %v", err)
				}
			})
			if err := rt.Start(startup); err != nil {
				t.Fatalf("Start: %v", err)
			}
			if received != startup {
				t.Fatalf("constructor context = %T (%v), want exact startup context %p", received, received, startup)
			}
			if value, err := rt.Value(ref); err != nil || value != 42 {
				t.Fatalf("mapped value = (%d, %v), want (42, nil)", value, err)
			}
		})
	}
}
