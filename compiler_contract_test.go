package component_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// These tests compile small external consumers so that the public API is
// checked at its real package boundary. The source snippets intentionally do
// not run a Runtime; this test is about constructor signatures and generic
// inference.
func TestCompilerContracts(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	version := exec.CommandContext(ctx, "go", "env", "GOVERSION")
	version.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOWORK=off", "GOFLAGS=")
	output, err := version.CombinedOutput()
	if err != nil {
		t.Fatalf("read consumer compiler version: %v\n%s", err, output)
	}
	if got, want := strings.TrimSpace(string(output)), runtime.Version(); got != want {
		t.Fatalf("consumer compiler version = %q, test binary version = %q; run tests with the Go toolchain on PATH", got, want)
	}

	tests := []struct {
		name string
		code string
		want bool
	}{
		{name: "accepts constructors and result inference", code: compilerContractAccepted, want: true},
		{name: "rejects wrong input type", code: `
package consumer

import "github.com/jacoelho/component"

var _ = component.MapValue(component.Value(1), func(string) int { return 0 })
`, want: false},
		{name: "rejects wrong input order", code: `
package consumer

import "github.com/jacoelho/component"

var _ = component.MapValue2(
	component.Value(1),
	component.Value("two"),
	func(string, int) int { return 0 },
)
`, want: false},
		{name: "rejects wrong arity", code: `
package consumer

import "github.com/jacoelho/component"

var _ = component.MapValue3(
	component.Value(1),
	component.Value(2),
	component.Value(3),
	func(int, int) int { return 0 },
)
`, want: false},
		{name: "rejects no-error constructor as Map", code: `
package consumer

import "github.com/jacoelho/component"

var _ = component.Map(component.Value(1), func(int) int { return 0 })
`, want: false},
		{name: "rejects error constructor as MapValue", code: `
package consumer

import "github.com/jacoelho/component"

var _ = component.MapValue(component.Value(1), func(int) (int, error) { return 0, nil })
`, want: false},
		{name: "rejects missing context", code: `
package consumer

import "github.com/jacoelho/component"

var _ = component.MapContext(component.Value(1), func(int) (int, error) { return 0, nil })
`, want: false},
		{name: "rejects context in the wrong position", code: `
package consumer

import (
	"context"
	"github.com/jacoelho/component"
)

var _ = component.MapContext2(
	component.Value(1),
	component.Value("two"),
	func(int, context.Context, string) (int, error) { return 0, nil },
)
`, want: false},
		{name: "rejects ProvideValue error result", code: `
package consumer

import "github.com/jacoelho/component"

var _ = component.ProvideValue(func() (int, error) { return 0, nil })
`, want: false},
		{name: "rejects Provide no-error result", code: `
package consumer

import "github.com/jacoelho/component"

var _ = component.Provide(func() int { return 0 })
`, want: false},
		{name: "rejects wrong ownership type", code: `
package consumer

import (
	"context"
	"github.com/jacoelho/component"
)

type first struct{}
func (*first) Start(context.Context) error { return nil }
func (*first) Stop(context.Context) error { return nil }
type second struct{}
func (*second) Start(context.Context) error { return nil }
func (*second) Stop(context.Context) error { return nil }

var _ = component.MapValue(component.Value(1), func(int) *first { return &first{} }, component.Managed[*second]())
`, want: false},
		{name: "rejects ownership without Lifecycle", code: `
package consumer

import "github.com/jacoelho/component"

type noLifecycle struct{}

var _ = component.ProvideValue(func() *noLifecycle { return &noLifecycle{} }, component.Managed[*noLifecycle]())
`, want: false},
		{name: "rejects direct concrete-to-interface constructor mismatch", code: `
package consumer

import "github.com/jacoelho/component"

type capability interface{ Value() int }
type concrete struct{}
func (*concrete) Value() int { return 1 }

func fromCapability(capability) int { return 0 }

var _ = component.MapValue(component.Value(&concrete{}), fromCapability)
`, want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runCompilerContract(t, test.code, test.want)
		})
	}
}

func runCompilerContract(t *testing.T, source string, wantCompile bool) {
	t.Helper()

	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("current repository path: %v", err)
	}

	moduleDir := t.TempDir()
	goMod := fmt.Sprintf(`module compiler-contract-consumer

go 1.27

require github.com/jacoelho/component v0.0.0

replace github.com/jacoelho/component => %q
`, repoRoot)
	if err := os.WriteFile(filepath.Join(moduleDir, "go.mod"), []byte(goMod), 0o600); err != nil {
		t.Fatalf("write consumer go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(moduleDir, "contract_test.go"), []byte(source), 0o600); err != nil {
		t.Fatalf("write consumer source: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(
		ctx,
		"go",
		"test",
		"-c",
		"-vet=off",
		"-o",
		filepath.Join(moduleDir, "consumer.test"),
		"./...",
	)
	cmd.Dir = moduleDir
	cmd.Env = append(os.Environ(),
		"GOTOOLCHAIN=local",
		"GOWORK=off",
		"GOPROXY=off",
		"GOSUMDB=off",
		"GOFLAGS=",
	)
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("compiler contract timed out: %v\n%s", ctx.Err(), output)
	}
	if wantCompile && err != nil {
		t.Fatalf("valid consumer failed to compile: %v\n%s", err, output)
	}
	if !wantCompile {
		if err == nil {
			t.Fatalf("invalid consumer compiled successfully")
		}
		var exitError *exec.ExitError
		if !errors.As(err, &exitError) {
			t.Fatalf("invalid consumer failed outside the compiler: %T: %v\n%s", err, err, output)
		}
	}
}

const compilerContractAccepted = `
package consumer

import (
	"context"
	"github.com/jacoelho/component"
)

func init() { panic("compiler contract source must not execute") }

type capability interface{ Value() int }

type concrete struct{ value int }

func (*concrete) Start(context.Context) error { return nil }
func (*concrete) Stop(context.Context) error { return nil }
func (value *concrete) Value() int { return value.value }

func accepted() {
	first := component.Value(2)
	second := component.Value(3)
	third := component.Value(5)
	fourth := component.Value(7)

	// The result type is inferred from the constructor and may also be
	// supplied explicitly as the first type argument.
	var unaryValue component.Ref[string] = component.MapValue(first, func(value int) string {
		return "value"
	})
	var explicitUnaryValue component.Ref[string] = component.MapValue[string](first, func(int) string {
		return "explicit"
	})
	var pairValue component.Ref[string] = component.MapValue2(first, second, func(a, b int) string {
		return "pair"
	})
	var explicitPairValue component.Ref[string] = component.MapValue2[string](first, second, func(a, b int) string {
		return "explicit-pair"
	})
	var tripleValue component.Ref[string] = component.MapValue3(first, second, third, func(a, b, c int) string {
		return "triple"
	})
	var explicitTripleValue component.Ref[string] = component.MapValue3[string](first, second, third, func(a, b, c int) string {
		return "explicit-triple"
	})
	var quadValue component.Ref[string] = component.MapValue4(first, second, third, fourth, func(a, b, c, d int) string {
		return "quad"
	})
	var explicitQuadValue component.Ref[string] = component.MapValue4[string](first, second, third, fourth, func(a, b, c, d int) string {
		return "explicit-quad"
	})

	var unaryError component.Ref[int] = component.Map(first, func(value int) (int, error) {
		return value, nil
	})
	var explicitUnaryError component.Ref[string] = component.Map[string](first, func(value int) (string, error) {
		return "explicit-error", nil
	})
	var pairError component.Ref[int] = component.Map2(first, second, func(a, b int) (int, error) {
		return a + b, nil
	})
	var explicitPairError component.Ref[string] = component.Map2[string](first, second, func(a, b int) (string, error) {
		return "explicit-pair-error", nil
	})
	var tripleError component.Ref[int] = component.Map3(first, second, third, func(a, b, c int) (int, error) {
		return a + b + c, nil
	})
	var quadError component.Ref[int] = component.Map4(first, second, third, fourth, func(a, b, c, d int) (int, error) {
		return a + b + c + d, nil
	})

	var unaryContext component.Ref[int] = component.MapContext(first, func(context.Context, int) (int, error) {
		return 1, nil
	})
	var explicitUnaryContext component.Ref[string] = component.MapContext[string](first, func(context.Context, int) (string, error) {
		return "explicit-context", nil
	})
	var pairContext component.Ref[int] = component.MapContext2(first, second, func(context.Context, int, int) (int, error) {
		return 1, nil
	})
	var tripleContext component.Ref[int] = component.MapContext3(first, second, third, func(context.Context, int, int, int) (int, error) {
		return 1, nil
	})
	var explicitTripleContext component.Ref[string] = component.MapContext3[string](first, second, third, func(context.Context, int, int, int) (string, error) {
		return "explicit-triple-context", nil
	})
	var quadContext component.Ref[int] = component.MapContext4(first, second, third, fourth, func(context.Context, int, int, int, int) (int, error) {
		return 1, nil
	})

	var providedValue component.Ref[int] = component.ProvideValue(func() int { return 1 })
	var providedError component.Ref[int] = component.Provide(func() (int, error) { return 1, nil })
	var providedContext component.Ref[int] = component.ProvideContext(func(context.Context) (int, error) { return 1, nil })

	owned := component.ProvideValue(func() *concrete { return &concrete{} }, component.Managed[*concrete]())
	ownedMapped := component.MapValue(owned, func(value *concrete) *concrete {
		return &concrete{value: value.value}
	}, component.Managed[*concrete]())

	// Interface adaptation stays an ordinary Go closure, so the assignment is
	// checked by the compiler rather than by component at runtime.
	var adapted component.Ref[capability] = component.MapValue(owned, func(value *concrete) capability {
		return value
	})

	_ = unaryValue
	_ = explicitUnaryValue
	_ = pairValue
	_ = explicitPairValue
	_ = tripleValue
	_ = explicitTripleValue
	_ = quadValue
	_ = explicitQuadValue
	_ = unaryError
	_ = explicitUnaryError
	_ = pairError
	_ = explicitPairError
	_ = tripleError
	_ = quadError
	_ = unaryContext
	_ = explicitUnaryContext
	_ = pairContext
	_ = tripleContext
	_ = explicitTripleContext
	_ = quadContext
	_ = providedValue
	_ = providedError
	_ = providedContext
	_ = ownedMapped
	_ = adapted
}
`
