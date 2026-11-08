package component_test

import (
	"testing"

	"github.com/jacoelho/component"
)

// BenchmarkNewStartStop measures one App lifecycle over a layered graph in
// which every node depends on two nodes of the previous layer.
func BenchmarkNewStartStop(b *testing.B) {
	const layers, width = 50, 20
	prev := make([]component.Ref[int], width)
	for i := range prev {
		prev[i] = component.Value(i)
	}
	for range layers {
		next := make([]component.Ref[int], width)
		for i := range next {
			next[i] = component.MapValue2(prev[i], prev[(i+1)%width], func(a, b int) int { return a + b })
		}
		prev = next
	}
	roots := make([]component.Root, width)
	for i, r := range prev {
		roots[i] = r
	}
	ctx := b.Context()
	b.ReportAllocs()
	for b.Loop() {
		app, err := component.New(ctx, roots)
		if err != nil {
			b.Fatal(err)
		}
		if err := app.Start(ctx); err != nil {
			b.Fatal(err)
		}
		if err := app.Stop(ctx); err != nil {
			b.Fatal(err)
		}
	}
}
