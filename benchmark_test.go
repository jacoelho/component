package component_test

import (
	"testing"

	"github.com/jacoelho/component"
)

// Construction of definitions is outside the measurement: these benchmarks
// isolate per-runtime graph validation and compilation, without user I/O.
func BenchmarkNew(b *testing.B) {
	const size = 1000
	b.Run("chain", func(b *testing.B) {
		root := component.Value(0)
		for range size {
			root = root.Map(func(value int) int { return value + 1 })
		}
		b.ReportAllocs()
		for b.Loop() {
			if _, err := component.New(component.RuntimeOptions{}, root); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("wide", func(b *testing.B) {
		source := component.Value(0)
		roots := make([]component.Root, size)
		for index := range roots {
			roots[index] = source.Map(func(value int) int { return value + 1 })
		}
		b.ReportAllocs()
		for b.Loop() {
			if _, err := component.New(component.RuntimeOptions{}, roots...); err != nil {
				b.Fatal(err)
			}
		}
	})
}
