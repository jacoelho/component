package component_test

import (
	"context"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"

	component "github.com/jacoelho/component"
)

type constructorContextKey struct{}

func TestRefRejectsTagOnlyConversions(t *testing.T) {
	type before = struct {
		Value int `json:"before"`
	}
	type after = struct {
		Value int `json:"after"`
	}
	from := reflect.TypeFor[component.Ref[before]]()
	to := reflect.TypeFor[component.Ref[after]]()
	if from.ConvertibleTo(to) {
		t.Fatal("references with different value types must not be convertible, including tag-only differences")
	}
}

func TestRefRemainsComparableForNonComparableValues(t *testing.T) {
	for _, typ := range []reflect.Type{
		reflect.TypeFor[component.Ref[[]byte]](),
		reflect.TypeFor[component.Ref[map[string]int]](),
		reflect.TypeFor[component.Ref[func()]](),
	} {
		if !typ.Comparable() {
			t.Errorf("%v must be comparable", typ)
		}
	}
}

func TestFourInputCompositionPreservesTypedOrder(t *testing.T) {
	a := component.Value("a")
	b := component.Value(2)
	c := component.Value(true)
	d := component.Value(byte('d'))
	ref := a.With(b).With(c).With(d).Map(func(gotA string, gotB int, gotC bool, gotD byte) string {
		return fmt.Sprintf("%s/%d/%t/%c", gotA, gotB, gotC, gotD)
	})

	rt, err := component.New(ref)
	if err != nil {
		t.Fatalf("New returned an error")
	}
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("Start returned an error")
	}
	got, err := rt.Value(ref)
	if err != nil {
		t.Fatalf("Value returned an error")
	}
	if got != "a/2/true/d" {
		t.Fatalf("four-input result = %q, want %q", got, "a/2/true/d")
	}
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("Stop returned an error")
	}
}

func TestContextAndErrorConstructorForms(t *testing.T) {
	ctxKey := constructorContextKey{}
	wantContext := context.WithValue(context.Background(), ctxKey, "startup")
	var seen context.Context
	provided := component.ProvideContext(func(ctx context.Context) (int, error) {
		seen = ctx
		return 3, nil
	})
	tried := provided.TryMap(func(value int) (string, error) {
		return fmt.Sprintf("%d", value), nil
	})
	contextMapped := tried.MapContext(func(ctx context.Context, value string) (string, error) {
		if ctx.Value(ctxKey) != "startup" {
			return "", fmt.Errorf("wrong context")
		}
		return value + "!", nil
	})

	rt, err := component.New(contextMapped)
	if err != nil {
		t.Fatalf("New returned an error")
	}
	if err := rt.Start(wantContext); err != nil {
		t.Fatalf("Start returned an error")
	}
	if seen != wantContext {
		t.Fatalf("context constructor did not receive the exact startup context")
	}
	got, err := rt.Value(contextMapped)
	if err != nil {
		t.Fatalf("Value returned an error")
	}
	if got != "3!" {
		t.Fatalf("context/error result = %q, want %q", got, "3!")
	}
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("Stop returned an error")
	}
}

func TestDeepChainUsesIterativeGraphTraversal(t *testing.T) {
	const depth = 5000
	ref := component.Value(0)
	for index := 1; index <= depth; index++ {
		ref = ref.Map(func(previous int) int { return previous + 1 })
	}
	rt, err := component.New(ref)
	if err != nil {
		t.Fatalf("New returned an error for a deep chain")
	}
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("Start returned an error for a deep chain")
	}
	got, err := rt.Value(ref)
	if err != nil {
		t.Fatalf("Value returned an error for a deep chain")
	}
	if got != depth {
		t.Fatalf("deep chain result = %d, want %d", got, depth)
	}
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("Stop returned an error for a deep chain")
	}
}

func TestDiamondAndWideGraphsDeduplicateSharedDefinitions(t *testing.T) {
	var created atomic.Int32
	base := component.Provide(func() *int {
		created.Add(1)
		value := 9
		return &value
	})
	left := base.Map(func(value *int) int { return *value + 1 })
	right := base.Map(func(value *int) int { return *value + 2 })
	diamond := left.With(right).Map(func(first, second int) int { return first + second })

	const width = 128
	roots := make([]component.Root, 0, width+1)
	roots = append(roots, diamond)
	for index := 0; index < width; index++ {
		offset := index
		roots = append(roots, base.Map(func(value *int) int { return *value + offset }))
	}
	rt, err := component.New(roots...)
	if err != nil {
		t.Fatalf("New returned an error for a wide graph")
	}
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("Start returned an error for a wide graph")
	}
	if got := created.Load(); got != 1 {
		t.Fatalf("shared source was constructed %d times, want 1", got)
	}
	if got, err := rt.Value(diamond); err != nil || got != 21 {
		t.Fatalf("diamond result = %d, error present=%t; want 21", got, err != nil)
	}
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("Stop returned an error for a wide graph")
	}
}

func TestIndependentSameTypeRefsRemainDistinct(t *testing.T) {
	first := component.Provide(func() int { return 17 })
	second := component.Provide(func() int { return 23 })
	combined := first.With(second).Map(func(a, b int) int { return a*100 + b })
	rt, err := component.New(combined)
	if err != nil {
		t.Fatalf("New returned an error")
	}
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("Start returned an error")
	}
	got, err := rt.Value(combined)
	if err != nil {
		t.Fatalf("Value returned an error")
	}
	if got != 1723 {
		t.Fatalf("same-type refs were merged: result=%d, want 1723", got)
	}
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("Stop returned an error")
	}
}
