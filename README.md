# Component

`component` builds a typed construction graph and runs its owned resources in
deterministic dependency order. A dependency is ready before a dependent
starts, and a dependent has finished stopping before its dependency is stopped.

The graph is ordinary Go code. Constructors receive ordinary values, so the
application keeps its own types, interfaces, closures, and resource policies.
References exist only in composition code.

## Install

Requires Go 1.27 or later.

```bash
go get github.com/jacoelho/component
```

## Quick start

This complete program uses a placeholder database to show construction, typed
value access, and cleanup. Replace its lifecycle methods with your resource's
readiness and shutdown logic.

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
	database := component.ProvideValue(openDatabase, component.Managed[*Database]())
	service := component.MapValue(database, newService)

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

`Value` supplies an already-created borrowed value. It never invokes a
function and never takes ownership:

```go
config := component.Value(existingConfig)
client := component.MapValue(config, client.New)
```

Use the package function matching the constructor's result shape:

```go
credentials := component.ProvideContext(func(ctx context.Context) (*Credentials, error) {
	return loadCredentials(ctx)
})

database := component.Provide(openDatabaseWithError)
service := component.Map(database, newServiceWithError)
```

For a context-taking constructor with no error result, adapt it explicitly at
the call site:

```go
mapped := component.MapContext(ref, func(ctx context.Context, value A) (B, error) {
	return NewValue(ctx, value), nil
})
```

The package functions take dependencies first, the constructor second, and
ownership options last. The result type is the first type parameter, followed
by dependency types. The compiler checks each dependency, constructor
argument, result, and ownership method set:

| Inputs | No error | Error | Context and error |
| --- | --- | --- | --- |
| None | `ProvideValue(create)` | `Provide(create)` | `ProvideContext(create)` |
| One | `MapValue(input, create)` | `Map(input, create)` | `MapContext(input, create)` |
| Two | `MapValue2(first, second, create)` | `Map2(first, second, create)` | `MapContext2(first, second, create)` |
| Three | `MapValue3(first, second, third, create)` | `Map3(first, second, third, create)` | `MapContext3(first, second, third, create)` |
| Four | `MapValue4(first, second, third, fourth, create)` | `Map4(first, second, third, fourth, create)` | `MapContext4(first, second, third, fourth, create)` |

The `2` forms through the `4` forms follow the same pattern and preserve the
constructor's argument order. A repeated reference remains a repeated
argument, while the graph still constructs and stops that definition once.

For more than four inputs, map a named ordinary value and continue with it:

```go
type ServiceInputs struct {
	Config *Config
	Logger Logger
	Store  Store
	Cache  Cache
}

inputs := component.MapValue4(config, logger, store, cache,
	func(c *Config, l Logger, s Store, cache Cache) ServiceInputs {
		return ServiceInputs{Config: c, Logger: l, Store: s, Cache: cache}
	})

service := component.MapValue2(inputs, jobs,
	func(in ServiceInputs, jobs JobConfig) *Service {
		return NewService(in, jobs)
	})
```

Use an intermediate named value when it represents a cohesive or reused
capability. The package surface does not add grouping methods or types merely
to shorten one constructor call.

Interface adaptation remains ordinary Go assignment:

```go
database := component.ProvideValue(openDatabase, component.Managed[*Database]())
store := component.MapValue(database, func(db *Database) Store {
	return db
})
service := component.MapValue(store, newService)
```

`store` is an unmanaged interface alias. `database` remains the lifecycle
owner. Pass `Managed[Store]` only when the mapping creates a distinct resource
that owns its own lifecycle; marking an alias as owned would violate the
fresh-resource contract.

Each `Ref[T]` is typed and comparable even when `T` is a slice, map, or
function. References of different `T` values cannot be explicitly converted.
Independent references with the same `T` remain distinct; swapping them in a
multi-input call changes the constructor arguments. Repeating one reference
supplies the same value at each position and constructs that definition once.
`New` checks zero or invalid references, nil constructors, duplicate ownership
options, and cycles before any constructor runs. Only references reachable from
the roots passed to `New` are included. A shared reference is constructed once
per runtime, even when several nodes use it.

## Ownership and lifecycle

A constructor without an ownership argument produces an unmanaged value. A
constructor with one `Ownership[T]` from `Managed[T]` produces an owned
resource. `T` implements the `Lifecycle` interface, whose `Start` and `Stop`
methods receive only the caller's context. `Start` is required, even for an
inert resource; its method can return nil. An unmanaged value stays unmanaged
even if it happens to implement `Lifecycle`:

```go
source := component.MapValue(sink, NewSource,
	component.Managed[*Source]())

named := component.Managed[*Source]()
named.Name = "source" // optional diagnostic name
namedSource := component.MapValue(sink, NewSource, named)
```

An owned factory must create a distinct resource for that runtime. It must not
return an input or another owned object. Go cannot prove freshness, so this is
an application contract. Use `Value` for an intentionally shared borrowed
object.

Ownership transfers only when a factory returns successfully. If a factory
returns an error, it must clean up its own partial acquisition, even if it also
returns a value. A successful managed factory must return a non-nil resource.

`ProvideValue` and `MapValue` only describe factories that do not return an
error. They do not make startup infallible: a factory may panic, and a managed
resource's `Start` or `Stop` may fail. Runtime errors retain the node context;
an owned value that was constructed before a later failure remains available
for cleanup.

Every owned `Stop` method must tolerate repeated calls and partial cleanup. A
nil result means the node is complete. An error leaves it pending; the next
caller `Stop` may retry it once, while successful stops are never repeated.
The library does not guess whether a third-party `Close` made progress or
translate an already-closed error. Adapt such APIs in application code.
Failed cleanup retains the node's dependencies and returns
`ErrCleanupPending`, detectable with `errors.Is`. Call `Stop` again to retry
with a suitable context; the quick start reports cleanup errors without
retrying.

The runtime allows one startup attempt and cannot be restarted. Overlapping
`Start` or `Stop` calls return `ErrBusy`. The application calls `Stop` after a
failed `Start` to release resources that were successfully constructed,
including resources whose `Start` method failed. Nodes run one at a time in
stable dependency order. A factory and its `Start` method form one handoff;
once that factory succeeds, its `Start` method still runs even if the
operation context is canceled while the factory is returning.

## Context, values, and limits

Context-taking factories and lifecycle methods receive exactly the context
supplied by the caller. Cancellation prevents the next node from being
dispatched. The current factory or lifecycle callback is allowed to finish,
and the operation cannot forcibly stop user code. Nil contexts are rejected
before runtime state changes. `Runtime.Value(ref)` is available only while a
runtime is successfully running and returns a typed value; it is not a
resolver for constructors. Stop using returned values before calling `Stop`.

Resources that launch background work must own their context and join that
work during shutdown; the startup context bounds startup. The runtime does not
run independent nodes concurrently, roll back startup, install signals or
timeouts, retry cleanup in the background, infer dependencies from closure
captures, or discover message topology. Queue admission, message limits,
worker shutdown, and provider-specific timeouts belong to the application
owner at the resource boundary. A callback that ignores cancellation can
determine operation latency.

## Complete examples and architecture

[`example_test.go`](example_test.go) shows lifecycle order in an executable
example. [`integration_test.go`](integration_test.go) contains a deterministic
source-to-processor-to-sink pipeline and a hermetic HTTP listener example. The
tests show readiness handshakes, graceful drain, caller-owned contexts, and
cleanup after failed startup.

[`ARCHITECTURE.md`](ARCHITECTURE.md) is the canonical architecture entry point.
