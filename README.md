# Component

`component` builds a typed construction graph and runs its owned resources in
dependency order. A dependency is ready before a dependent starts, and a
dependent has finished stopping before its dependency is stopped.

The graph is ordinary Go code. Constructors receive ordinary values, so the
application keeps its own types, interfaces, closures, and resource policies.
References exist only in composition code.

## Install

Requires Go 1.27 or later.

```bash
go get github.com/jacoelho/component
```

## Quick start

```go
type Database struct{}
type Service struct{ database *Database }

func openDatabase() *Database { return &Database{} }
func newService(database *Database) *Service {
	return &Service{database: database}
}

func (*Database) Start(context.Context) error { return nil }
func (*Database) Stop(context.Context) error  { return nil }

database := component.Provide(openDatabase, component.Managed[*Database]())
service := database.Map(newService)

runtime, err := component.New(component.RuntimeOptions{}, service)
if err != nil {
	return err
}

startCtx, cancelStart := context.WithTimeout(context.Background(), 10*time.Second)
defer cancelStart()
if err := runtime.Start(startCtx); err != nil {
	// Start does not roll back acquired resources. The application still owns
	// cleanup and supplies the shutdown policy.
	stopCtx, cancelStop := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelStop()
	return errors.Join(err, runtime.Stop(stopCtx))
}

stopCtx, cancelStop := context.WithTimeout(context.Background(), 10*time.Second)
defer cancelStop()
return runtime.Stop(stopCtx)
```

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

The same three forms are available on `Inputs2`, `Inputs3`, and `Inputs4` for
one through four typed inputs. Compose a normal Go value for a larger
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

Every owned `Stop` method must tolerate repeated calls and partial cleanup. A
nil result means the node is complete. An error leaves it pending; the next
caller `Stop` may retry it once, while successful stops are never repeated.
The library does not guess whether a third-party `Close` made progress or
translate an already-closed error. Adapt such APIs in application code.

The runtime is one-shot. The application calls `Stop` after a failed `Start`
to release resources that were successfully constructed, including resources
whose `Start` method failed. The runtime waits for all user callbacks before an
operation returns and bounds concurrent callbacks with `RuntimeOptions`:

```go
runtime, err := component.New(component.RuntimeOptions{Parallelism: 4}, root)
```

The default parallelism is one. A negative value is invalid.

## Context and values

Context-taking factories and lifecycle methods receive exactly the context
supplied by the caller. Cancellation prevents new work from being dispatched and waits for
callbacks already in flight; it cannot forcibly stop user code. Nil contexts
are rejected before runtime state changes. `Runtime.Value(ref)` is available
only while a runtime is successfully running and returns a typed value; it is
not a resolver for constructors.

## Complete examples and architecture

[`integration_test.go`](integration_test.go) contains a deterministic
source-to-processor-to-sink pipeline and a hermetic HTTP listener example. The
tests show readiness handshakes, graceful drain, caller-owned contexts, and
cleanup after failed startup.

[`ARCHITECTURE.md`](ARCHITECTURE.md) is the canonical architecture entry point.
[`REWRITE_PLAN.md`](REWRITE_PLAN.md) is the historical rewrite and review
record.
