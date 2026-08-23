# Component

`component` coordinates Go resource owners in dependency order. Dependencies
are configured and started before their dependents; dependents are stopped
before their dependencies. Construction and dependency injection remain
ordinary Go.

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

- The graph records lifecycle ordering, not values. Construct objects and pass
  their dependencies with constructors, fields, or interfaces.
- `NewNode[T]` creates a distinct identity tied to lifecycle type `T`. Its
  string label is used only in diagnostics.
- `Register` declares an owner and the owners it depends on. Independent owners
  at the same dependency level run concurrently.
- `Compile` rejects missing dependencies and cycles. A successful compile
  consumes the registry and returns a one-shot runtime.
- `Start` configures the complete graph before starting any owner. It never
  calls `Stop`; the application decides whether and how to clean up a failed
  start.
- `Stop` runs in reverse dependency order. Failed owners remain eligible for a
  later retry, while owners already stopped successfully are not called again.
- A runtime cannot restart after a `Start` attempt or a `Stop` call.

## Simple example

Assume `Database` and `Server` are application types that implement
`Lifecycle`. The server receives the database through an ordinary constructor;
the graph records only their lifecycle order.

```go
database := NewDatabase(databaseConfig)
server := NewServer(serverConfig, database)

databaseNode := component.NewNode[*Database]("database")
serverNode := component.NewNode[*Server]("server")

registry := component.NewRegistry()
if err := registry.Register(databaseNode, database); err != nil {
	return err
}
if err := registry.Register(serverNode, server, databaseNode); err != nil {
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

## Complex example

[`ExampleRuntime_gracefulHTTPShutdown`](example_test.go#L268) registers an HTTP
handler and server, owns the listener and serving goroutine, handles OS signals,
drains in-flight requests, and uses independent startup and shutdown contexts.
