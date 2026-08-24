# Component

`component` coordinates Go resource owners in dependency order. Dependencies
are configured and started before their dependents; dependents are stopped
before their dependencies. Owners may be wired manually or constructed from
typed provider functions after the complete graph has been validated.

## Install

Requires Go 1.27 or later.

```bash
go get github.com/jacoelho/component
```

## Lifecycle interface

Register types that implement `Lifecycle`:

```go
type Lifecycle interface {
	Configure(context.Context) error
	Start(context.Context) error
	Stop(context.Context) error
}
```

- `Configure` prepares reversible, non-live state.
- `Start` makes the owner live and returns when it is ready.
- `Stop` releases configured or started state. It must support partial setup
  and retries after a failed stop.

A lifecycle owner is responsible for the resources and goroutines it creates.
Callbacks must observe context cancellation. `LifecycleFuncs` adapts functions
or external APIs to this interface; nil callbacks are no-ops.

## Principles and features

- `Register` adds an already constructed owner and explicit lifecycle-ordering
  edges.
- `Provide` adds an inert constructor. Its parameters resolve to registered
  lifecycle owners and also create lifecycle-ordering edges.
- `NewNode[T]` creates a distinct identity tied to lifecycle type `T`. Its
  string label is used only in diagnostics.
- `Compile` validates all types, bindings, references, and cycles before
  invoking any provider constructor. Independent owners at the same dependency
  level run concurrently during lifecycle callbacks.
- `Start` configures the complete graph before starting any owner. It never
  calls `Stop`; the application decides whether and how to clean up a failed
  start.
- `Stop` runs in reverse dependency order. Failed owners remain eligible for a
  later retry, while owners already stopped successfully are not called again.
- A runtime cannot restart after a `Start` attempt or a `Stop` call.

## Provider composition

Assume `Database` and `Server` are application types that implement
`Lifecycle`:

```go
registry := component.NewRegistry()
_, err := registry.Provide("database", func() *Database {
	return NewDatabase(databaseConfig)
})
if err != nil {
	return err
}
_, err = registry.Provide("server", func(database *Database) (*Server, error) {
	return NewServer(serverConfig, database)
})
if err != nil {
	return err
}

runtime, err := registry.Compile()
if err != nil {
	return err
}
if err := runtime.Start(startCtx); err != nil {
	return err
}

// The application decides when to stop and supplies a separate context.
return runtime.Stop(stopCtx)
```

Provider constructors must return `T` or `(T, error)`, where `T` implements
`Lifecycle`. Constructors run sequentially in dependency order during
`Compile`; they must be finite and inert. Acquire resources and start
goroutines in `Configure` or `Start`, where a context and runtime cleanup are
available. Constructor parameters are borrowed owners: a dependent must stop
only resources it creates, never an injected owner.

For each constructor parameter, resolution uses:

1. An explicit `Bind[T]`.
2. One owner declared as exactly `T`.
3. For an interface `T`, one owner whose declared type implements `T`.

Missing and ambiguous parameters fail `Compile` before any provider constructor
runs. `Provide` still rejects an invalid local constructor signature or
`orderAfter` reference immediately, and `Bind` rejects an invalid local binding
immediately. When several owners implement an interface, bind the intended
owner:

```go
loggerRef, err := registry.Provide("logger", NewLogger)
if err != nil {
	return err
}
if err := registry.Bind[Logger](loggerRef); err != nil {
	return err
}
```

`Bind` selects injection only: every registered owner is still part of the
lifecycle runtime. Register only the implementations that should run. Labels
are diagnostic and never affect resolution.

Manual registration remains available when the application needs the value
before `Compile`:

```go
registry := component.NewRegistry()
database := NewDatabase(databaseConfig)
server := NewServer(serverConfig, database)
databaseNode := component.NewNode[*Database]("database")
serverNode := component.NewNode[*Server]("server")

if err := registry.Register(databaseNode, database); err != nil {
	return err
}
if err := registry.Register(serverNode, server, databaseNode); err != nil {
	return err
}
```

Manual declarations participate in provider resolution using the static `T`
from `NewNode[T]`, never the value's dynamic type. No runtime value lookup is
provided.

Structural compile failures leave the registry editable and invoke no provider
constructor. Once validation succeeds, the registry is consumed before
construction begins. A constructor error, panic, `runtime.Goexit`, or nil
lifecycle returns `ErrConstruction`, no runtime, and invokes no lifecycle
callback. Manually constructed values remain the caller's cleanup
responsibility whenever `Compile` returns an error.

## Complex example

[`ExampleRuntime_gracefulHTTPShutdown`](example_test.go#L299) registers an HTTP
handler and server, owns the listener and serving goroutine, handles OS signals,
drains in-flight requests, and uses independent startup and shutdown contexts.
