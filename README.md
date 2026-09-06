# Component

`component` builds a typed construction graph and runs its owned resources in
deterministic dependency order. A dependency is ready before a dependent
starts, and a dependent has finished stopping before its dependency is
stopped.

The graph is ordinary Go code. Constructors receive ordinary values, so the
application keeps its own types, interfaces, closures, and resource policies.
References exist only in composition code.

## Install

Requires Go 1.27 or later.

```bash
go get github.com/jacoelho/component
```

## Quick start

This complete program uses a placeholder database to show construction,
typed value access, and cleanup. Replace its lifecycle methods with your
resource's readiness and shutdown logic.

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jacoelho/component"
)

type Database struct{}
type Service struct{ database *Database }

func openDatabase() *Database { return &Database{} }
func newService(database *Database) *Service {
	return &Service{database: database}
}

func (*Database) Start(context.Context) error { return nil }
func (*Database) Stop(context.Context) error  { return nil }

func run() (err error) {
	database := component.Provide(openDatabase, component.Managed[*Database]())
	service := database.Map(newService)

	runtime, err := component.New(service)
	if err != nil {
		return err
	}
	defer func() {
		// Cleanup also runs after failed startup, with its own context.
		stopCtx, cancelStop := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelStop()
		err = errors.Join(err, runtime.Stop(stopCtx))
	}()

	startCtx, cancelStart := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelStart()
	if err := runtime.Start(startCtx); err != nil {
		return err
	}

	value, err := runtime.Value(service)
	if err != nil {
		return err
	}
	fmt.Println("service ready:", value.database != nil)
	return nil
}

func main() {
	if err := run(); err != nil {
		panic(err)
	}
}
```

The program prints `service ready: true`. The database starts before the
service is constructed. Both become available only after `runtime.Start`
succeeds; deferred cleanup runs after the service is used.

`Start` and `Stop` receive independent caller-created contexts. The library
does not install signal handlers, create replacement contexts, retry work in
the background, or provide a `Run` helper.

## Construction

`Value` supplies an existing borrowed value. It never invokes a function and
never takes ownership:

```go
config := component.Value(existingConfig)
client := config.Map(client.New)
```

Use a no-error constructor with `Provide` or `Map`, an error-returning
constructor with `TryProvide` or `TryMap`, and a context-taking acquisition
with `ProvideContext` or `MapContext`:

```go
credentials := component.ProvideContext(func(ctx context.Context) (*Credentials, error) {
	return loadCredentials(ctx)
})

database := component.TryProvide(openDatabaseWithError)
service := database.TryMap(newServiceWithError)
```

For an existing context-taking constructor with no error result, keep the
adaptation explicit at the call site:

```go
mapped := ref.MapContext(func(ctx context.Context, value A) (B, error) {
	return NewValue(ctx, value), nil
})
```

`Ref` accepts one typed input. Use `With` to group two, three, or four inputs;
the resulting `Inputs2`, `Inputs3`, and `Inputs4` expose the same three mapping
forms. Compose a normal Go value for a larger
constructor instead of using reflection:

```go
storage := store.With(cache).With(logger).Map(func(s Store, c Cache, l Logger) StorageInputs {
	return StorageInputs{Store: s, Cache: c, Logger: l}
})
service := storage.With(httpConfig).With(jobConfig).Map(func(
	s StorageInputs,
	h HTTPConfig,
	j JobConfig,
) *Service {
	return NewLargeService(s.Store, s.Cache, s.Logger, h, j)
})
```

Interface adaptation remains ordinary Go assignment inside a closure:

```go
handler := database.Map(func(db *Database) *Handler {
	return NewHandler(db) // NewHandler accepts the Store interface.
})
```

Each `Ref[T]` is typed and comparable even when `T` is a slice, map, or
function. References of different `T` values cannot be explicitly converted.
The compiler checks arity, input/output types, closure bodies, and lifecycle
method sets. `New` checks graph validity before any constructor runs.
Only references reachable from the roots passed to `New` are included. A
shared reference is constructed once per runtime, even if several nodes use it.

## Ownership and lifecycle

A constructor without an ownership argument produces an unmanaged value. A
constructor with one `Ownership[T]` from `Managed[T]` produces an owned
resource. `T` implements the `Lifecycle` interface, whose `Start` and `Stop`
methods receive only the caller's context. `Start` is required, even for an
inert resource; its method can return nil. An unmanaged value stays unmanaged
even if it happens to implement `Lifecycle`:

```go
source := sink.Map(NewSource, component.Managed[*Source]())

named := component.Managed[*Source]()
named.Name = "source" // optional diagnostic name
namedSource := sink.Map(NewSource, named)
```

An owned factory must create a distinct resource for that runtime. It must not
return an input or another owned object. Go cannot prove freshness, so this is
an application contract. Use `Value` for an intentionally shared borrowed
object.

Ownership transfers only when a factory returns successfully. If a factory
returns an error, it must clean up its own partial acquisition, even if it also
returns a value. A successful managed factory must return a non-nil resource.

Every owned `Stop` method must tolerate repeated calls and partial cleanup. A
nil result means the node is complete. An error leaves it pending; the next
caller `Stop` may retry it once, while successful stops are never repeated.
The library does not guess whether a third-party `Close` made progress or
translate an already-closed error. Adapt such APIs in application code.
Failed cleanup retains the node's dependencies and returns
`ErrCleanupPending`, detectable with `errors.Is`. Call `Stop` again to retry
with a suitable context; the quick start reports cleanup errors without retrying.

The runtime allows one startup attempt and cannot be restarted. Overlapping
`Start` or `Stop` calls return `ErrBusy`. The application calls `Stop` after a
failed `Start` to release resources that were successfully constructed,
including resources whose `Start` method failed. Nodes run one at a time in
stable dependency order. A factory and its `Start` method form one handoff;
once that factory succeeds, its `Start` method still runs even if
the operation context is canceled while the factory is returning.

## Context and values

Context-taking factories and lifecycle methods receive exactly the context
supplied by the caller. Cancellation prevents the next node from being
dispatched. The current factory or lifecycle callback is allowed to finish,
and the operation cannot forcibly stop user code. Nil contexts
are rejected before runtime state changes. `Runtime.Value(ref)` is available
only while a runtime is successfully running and returns a typed value; it is
not a resolver for constructors.
The reference must belong to that runtime's graph. Stop using returned values
before calling `Stop`. Resources that launch background work must own its
context and join that work during shutdown; the startup context bounds startup.

## Complete examples and architecture

[`example_test.go`](example_test.go) shows the lifecycle order in an executable
example. [`integration_test.go`](integration_test.go) contains a deterministic
source-to-processor-to-sink pipeline and a hermetic HTTP listener example. The
tests show readiness handshakes, graceful drain, caller-owned contexts, and
cleanup after failed startup.

[`ARCHITECTURE.md`](ARCHITECTURE.md) is the canonical architecture entry point.
