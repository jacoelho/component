# Component

`component` wires a Go application as a typed graph of constructors and runs
it. It builds each value once, starts values after the values they depend on,
and stops them in reverse order. The compiler checks every dependency and
every lifecycle hook, without reflection or string keys.

Constructors stay ordinary Go functions, and lifecycle methods attach as
method expressions such as `(*sql.DB).Close`. Your types do not implement any
interface from this package.

## Install

Requires Go 1.27 or later.

```bash
go get github.com/jacoelho/component
```

## Quick start

A store opens, is pinged at start and closed at stop. A worker uses it and
runs until it is stopped:

```go
dsn := component.Value("postgres://localhost/app")
store := component.Map(dsn, OpenStore,
	component.OnStart((*Store).Ping),
	component.OnClose((*Store).Close))
worker := component.MapValue(store, NewWorker,
	component.Go((*Worker).Run))

ctx := context.Background()
app, err := component.New(ctx, []component.Root{worker})
if err != nil {
	log.Fatal(err)
}
if err := app.Start(ctx); err != nil {
	log.Print(err)
}
if err := app.Stop(ctx); err != nil {
	log.Print(err)
}
```

It prints:

```text
open store
ping store
worker stopped
close store
```

`OpenStore`, `NewWorker` and the methods are plain functions; the full code is
the package `Example` in [`example_test.go`](example_test.go).

`New` runs the constructors, `Start` runs the start hooks and launches tasks,
and `Stop` cancels the worker's context, waits for it, then closes the store.
The store closes last because the worker depends on it.

## Run a service

A service builds the graph, starts it, waits for a signal or for a task to
exit, then stops within a deadline:

```go
func runService() (err error) {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	handler := component.Value[http.Handler](http.NotFoundHandler())
	srv := component.MapValue(handler, func(h http.Handler) *http.Server {
		return &http.Server{Addr: ":8080", Handler: h, ReadHeaderTimeout: 5 * time.Second}
	}, component.Serve())

	app, err := component.New(ctx, []component.Root{srv})
	// Stop also cleans up after a failed New or Start, so defer it first.
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err = errors.Join(err, app.Stop(stopCtx))
	}()
	if err != nil {
		return err
	}
	if err := app.Start(ctx); err != nil {
		return err
	}
	if err := app.Wait(ctx); ctx.Err() == nil {
		return err // a task exited before any signal arrived
	}
	return nil // a signal is a normal shutdown
}
```

Call `Stop` even when `New` or `Start` fails: neither rolls back what it
built or started, and `New` always returns an App to stop. Deferring `Stop`
before checking `New`'s error keeps every path covered. The library installs no signal handlers and sets no timeouts; every
deadline comes from a context you pass.

`Serve` listens on `Addr` during `Start`, so an address already in use fails
`Start`. It runs `Serve` as a task and calls `Shutdown` at stop, falling back
to `Close` once the stop context has expired.

## Declare nodes

Each call declares one node and returns a `Ref[T]`. The refs a constructor
takes are its dependencies, so a node can only use nodes that already exist
and the graph cannot contain a cycle.

Pick the function by the constructor's result:

| Constructor shape | No inputs | One input | 2–8 inputs |
| --- | --- | --- | --- |
| `func(...) T` | `ProvideValue` | `MapValue` | `MapValue2` … `MapValue8` |
| `func(...) (T, error)` | `Provide` | `Map` | `Map2` … `Map8` |
| `func(context.Context, ...) (T, error)` | `ProvideContext` | `MapContext` | `MapContext2` … `MapContext8` |

Inputs come first, in the constructor's argument order, then the
constructor, then options. A context-taking constructor receives the context
given to `New`. `Value` declares a value you already have; the App borrows it
and never closes it.

A ref is an identity, not a type key. Two calls with the same constructor
create two nodes, and swapping two refs of the same type swaps the arguments.
A ref used by several nodes is built once per App.

For a constructor with more than eight inputs, build a named input struct in
its own node, or have the wiring code pass a struct of refs.

## Attach lifecycle hooks

Options attach to the call that creates a value, so the owner of a resource
decides how it starts and stops:

| Option | Runs |
| --- | --- |
| `OnStart(f)` | during `Start`, after every dependency has started |
| `Go(f)` | as a goroutine after the node's start hooks succeed; its context is cancelled at the node's turn in `Stop` |
| `OnDrain(f)` | at the beginning of `Stop`, before anything stops, if the node's start began |
| `OnStop(f)` | during `Stop`, if the node's start began |
| `OnClose(f)` | during `Stop`, for every built node, started or not |
| `Serve()` | listen, serve and shut down an `*http.Server` |
| `Label(name)` | names the node in errors and `Plan`; the default is the constructor's function name |

`OnStart`, `OnStop`, `OnDrain` and `Go` take `func(T, context.Context) error`,
which fits method expressions such as `(*sql.DB).PingContext`. `OnClose` takes
`func(T) error`, which fits `(*sql.DB).Close`. A hook for the wrong type does
not compile.

A node's start has begun once `Start` reaches it, even if one of its start
hooks then fails, so stop and drain hooks must tolerate a failed start.

A node's start hooks run in option order. Its stop and close hooks run in
reverse option order, like deferred calls.

## How order is decided

A node starts after all of its dependencies and stops before any of them.
Nodes with no path between them have no guaranteed order relative to each
other.

There is no way to declare an order without a dependency. When one component
must stop before another, make it depend on what it actually uses. An HTTP
API that submits jobs should take the worker's submitter as an input. The
server then stops before the worker drains, and the dependency is visible in
the constructor's signature.

When a constructor needs a dependency that is already running, wrap the input
in `Started`. `New` starts that dependency, and everything it depends on,
before calling the constructor:

```go
migrated := component.MapValue(component.Started(store), func(*Store) bool {
	fmt.Println("migrate")
	return true
})
```

## Split the graph across packages

A package exposes its part of the graph as a function from refs to refs:

```go
type StorageRefs struct {
	Store  component.Ref[*Store]
	Worker component.Ref[*Worker]
}

func StorageGraph(dsn component.Ref[string]) StorageRefs {
	store := component.Map(dsn, OpenStore,
		component.OnStart((*Store).Ping),
		component.OnClose((*Store).Close))
	worker := component.MapValue(store, NewWorker, component.Go((*Worker).Run))
	return StorageRefs{Store: store, Worker: worker}
}
```

Other packages take `StorageRefs.Store` as an input. Refs carry their edges,
so the order holds across packages without either package importing the
other. Call `StorageGraph` once and pass its refs on; a second call creates a
second store. Domain packages do not need to import `component` if their
graph functions live in a separate wiring package.

## Test with replacements

`Replace` substitutes a value for a node. Its constructor and hooks never run,
and nodes reachable only through it are never built. With the quick start's
`store` and `worker`:

```go
app, err := component.New(context.Background(), []component.Root{worker},
	component.Replace(store, &Store{dsn: "memory"}))
if err != nil {
	log.Fatal(err)
}
w, _ := component.Get(app, worker)
fmt.Println(w.store.dsn) // memory
```

The replacement is type-checked against the ref. `Get` reads any node's value
from a successful `New` until `Stop` begins. To test part of a graph, pass
only its refs as roots. `Plan` lists every node's label and dependencies in
build order: each node after its dependencies. `Stop` runs that order in
reverse.

## Shutdown and errors

`Stop` runs every drain hook first, then visits nodes in reverse order. For
each node it cancels the node's task context, runs its stop and close hooks,
then waits for its tasks. Hooks run before the wait, so `Shutdown` is what
ends a task blocked in `Serve`.

`Stop` continues after errors and runs each hook at most once. Once the stop
context is done, it stops waiting for tasks and reports each one still running
as `ErrAbandoned`, but still calls the remaining hooks. Calling `Stop` again
returns the first call's result.

A `Stop` issued while `Start` is running cancels `Start`'s context and waits
for the running hook to return. Do not call `Stop` or `Start` from a hook or a
task of the same App, because it deadlocks. A task that wants the App to stop
returns instead, and `Wait` reports it.

If a declaration is invalid, or a constructor fails or panics, `New` stops
building and returns the error with a failed App. `Start`, `Wait` and `Get`
on it return `ErrBuildFailed`; `Stop` closes what was built and stops what a
`Started` input had started, within the stop context. Every node failure is a `*NodeError` carrying the node's label and `Phase`,
and the original error stays reachable with `errors.Is` and `errors.As`.
Panics become a `*PanicError` with the stack.

## Limits

- Constructors and hooks run one at a time. Tasks run concurrently, one
  goroutine each.
- An App is single-use: it starts once and cannot restart after `Stop`.
- A constructor or hook that calls `runtime.Goexit`, as `t.FailNow` does,
  ends the goroutine that called `New`, `Start` or `Stop`. In a task it is
  reported as an exit.
- `Serve` does not report the address it bound, so with port 0 in `Addr` you
  cannot learn which port it chose.

## More

- [`ARCHITECTURE.md`](ARCHITECTURE.md): the full lifecycle contract, the
  rejected alternatives and open questions.
- `go doc github.com/jacoelho/component`: the API reference.
- [`LICENSE`](LICENSE).
