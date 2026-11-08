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

// These tests compile small consumers in a separate module, so the API is
// checked at the boundary its callers see. The consumers never run.
func TestCompilerContracts(t *testing.T) {
	t.Parallel()
	requireLocalToolchain(t)
	tests := []struct {
		name string
		body string
		want bool
	}{
		{name: "accepts the documented call shapes", body: acceptedConsumer, want: true},
		{name: "rejects a wrong input type", body: `
var _ = component.MapValue(component.Value(1), func(string) int { return 0 })`},
		{name: "rejects swapped inputs", body: `
var _ = component.MapValue2(component.Value(1), component.Value("two"), func(string, int) int { return 0 })`},
		{name: "rejects a missing input", body: `
var _ = component.MapValue2(component.Value(1), func(int, int) int { return 0 })`},
		{name: "rejects a hook for another type", body: `
var _ = component.ProvideValue(func() int { return 0 },
	component.OnStart(func(string, context.Context) error { return nil }))`},
		{name: "rejects an option for another type", body: `
var _ = component.ProvideValue(func() int { return 0 }, component.Label[string]("x"))`},
		{name: "rejects a hook without an error result", body: `
var _ = component.ProvideValue(func() int { return 0 }, component.OnClose(func(int) {}))`},
		{name: "rejects Started of another type", body: `
var _ = component.MapValue(component.Started(component.Value(1)), func(string) int { return 0 })`},
		{name: "rejects Replace with another type", body: `
var _ = component.Replace(component.Value(1), "one")`},
		{name: "rejects converting refs of tag-only different types", body: `
type tagged struct{ V int ` + "`json:\"v\"`" + ` }
type plain struct{ V int }
var _ = component.Ref[plain](component.Value(tagged{}))`},
		{name: "rejects implementing In outside the package", body: `
type fake struct{}
func (fake) input() (component.Ref[int], bool) { return component.Ref[int]{}, false }
var _ = component.MapValue[int, int](fake{}, func(int) int { return 0 })`},
		{name: "rejects implementing Root outside the package", body: `
type fake struct{}
var _ = []component.Root{fake{}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			compileConsumer(t, tt.body, tt.want)
		})
	}
}

const acceptedConsumer = `
type db struct{}
func (*db) Ping(context.Context) error { return nil }
func (*db) Close() error             { return nil }
type repo interface{ Find() int }
type sqlRepo struct{ d *db }
func (sqlRepo) Find() int { return 1 }

func accepted() {
	cfg := component.Value("dsn")
	d := component.Map(cfg, func(string) (*db, error) { return &db{}, nil },
		component.OnStart((*db).Ping), component.OnClose((*db).Close), component.Label[*db]("db"))
	// Started stands wherever a Ref is accepted and infers the same types.
	var r component.Ref[repo] = component.MapValue(component.Started(d), func(d *db) repo { return sqlRepo{d} })
	srv := component.MapValue(r, func(repo) *http.Server { return &http.Server{} }, component.Serve(),
		component.OnDrain(func(*http.Server, context.Context) error { return nil }))
	worker := component.MapContext2(cfg, r, func(context.Context, string, repo) (int, error) { return 0, nil },
		component.Go(func(int, context.Context) error { return nil }))
	app, _ := component.New(context.Background(), []component.Root{srv, worker}, component.Replace(r, repo(sqlRepo{})))
	var _ repo
	_, _ = component.Get(app, r)
}
`

func compileConsumer(t *testing.T, body string, wantCompile bool) {
	t.Helper()
	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	dir := t.TempDir()
	goMod := fmt.Sprintf("module consumer\n\ngo 1.27\n\nrequire github.com/jacoelho/component v0.0.0\n\nreplace github.com/jacoelho/component => %q\n", repoRoot)
	src := "package consumer\n\nimport (\n\t\"context\"\n\t\"net/http\"\n\n\t\"github.com/jacoelho/component\"\n)\n\nvar _ context.Context\nvar _ http.Handler\n" + body + "\n"
	for name, content := range map[string]string{"go.mod": goMod, "consumer.go": src} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOWORK=off", "GOPROXY=off", "GOSUMDB=off", "GOFLAGS=")
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("compile timed out: %v\n%s", ctx.Err(), out)
	}
	var exitErr *exec.ExitError
	switch {
	case wantCompile && err != nil:
		t.Fatalf("valid consumer failed to compile: %v\n%s", err, out)
	case !wantCompile && err == nil:
		t.Fatalf("invalid consumer compiled:\n%s", body)
	case !wantCompile && !errors.As(err, &exitErr):
		t.Fatalf("compiler did not run: %v\n%s", err, out)
	case !wantCompile && !strings.Contains(string(out), "consumer.go"):
		t.Fatalf("failure is not a compile error in the consumer:\n%s", out)
	}
}

func requireLocalToolchain(t *testing.T) {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), "go", "env", "GOVERSION").Output()
	if err != nil {
		t.Fatalf("go env GOVERSION: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != runtime.Version() {
		t.Fatalf("go on PATH is %s, test binary is %s; run with the same toolchain", got, runtime.Version())
	}
}
