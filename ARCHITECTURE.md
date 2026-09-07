# Component architecture

This document is the canonical entry point for the current architecture. It
records the decisions that must remain true as the code evolves.

## Capability and ownership

`component` owns two capabilities:

1. It records an immutable, typed construction graph and derives the ordering
   needed for startup and shutdown.
2. It owns one runtime instance of each reachable definition and runs
   construction, readiness, and cleanup in deterministic serial order.

Application code owns resource policy. It defines resource types, constructors,
readiness handshakes, queue admission, drain or abort behavior, and idempotent
cleanup. The library does not discover lifecycle methods by name, resolve
values dynamically, or infer hidden closure captures.

`Ref[T]` is a composition value, not an application dependency. `Value` records
an already-created borrowed value. `ProvideValue`, `Provide`, and
`ProvideContext` create zero-input definitions. `MapValue`, `Map`, and
`MapContext`, including their `2` through `4` forms, create typed dependent
definitions. Each function takes dependencies first, its constructor second,
and ownership options last. The result type is the first type parameter,
followed by dependency types.

These package functions are typed wrappers over the same internal definition
creation path. A reference is one identity even when its constructor or output
type matches another reference. Different value types cannot be converted into
one another. A phantom pointer array to the named generic `valueCell[T]`
preserves that boundary, including for anonymous structs that differ only in
tags, while keeping references comparable for slice, map, and function values.
A direct `*T` marker would permit tag-only conversions and break typed runtime
retrieval.

An unmanaged definition stays unmanaged even if its result implements
`Lifecycle`. `Managed[T]` is the explicit ownership declaration and requires
`T` to implement:

```go
type Lifecycle interface {
	Start(context.Context) error
	Stop(context.Context) error
}
```

`Managed[T]` requires `Start` even for an inert resource; an inert method can
return nil. `Stop` owns all retry and partial-cleanup behavior. An unmanaged
value remains unmanaged even if its type implements `Lifecycle`.

Ownership is attached when the definition is created. The factory that creates
an object is therefore the only owner that can declare its stop policy. A
concrete-to-interface mapping that merely aliases an existing value should be
unmanaged; the concrete definition retains ownership. Applying `Managed` to
such an alias would make the alias claim a fresh resource that it did not
create.

An owned factory must return a fresh resource for each runtime and must not
return an input or another owned resource. This freshness rule is an
application invariant: arbitrary Go values and aliases cannot be proven fresh
by the type system. Existing shared objects remain borrowed through `Value`.

## Graph and execution

Definitions are immutable and reachable only through the roots passed to
`New`. A multi-input package function preserves constructor argument order and
repeated input positions while the compiler deduplicates repeated graph edges.
The repeated value is supplied to each position, but the shared definition is
constructed, started, and stopped once per runtime. Independent references with
the same output type remain distinct; swapping them changes the constructor
arguments.

The package functions put dependencies at the construction site. The public
API has no grouping methods or input types because no reusable grouped value
has been demonstrated. When a constructor has more than four inputs, or when
a cohesive dependency set is reused, the application may map a named ordinary
value and use that value in later package calls. The named value is then an
explicit graph capability with ordinary Go fields and methods.

`New` validates every reachable definition before user code runs. It rejects
zero or invalid references, nil functions, duplicate ownership declarations,
invalid ownership values, and cycles. The runtime then materializes one
instance per reachable definition.
Independent runtimes have independent owned values. Pure mapping definitions
remain in the graph so that a dependent's lifetime keeps its source alive.

`New` computes a deterministic topological order once. Startup walks it
forward, so each node runs only after all of its inputs are ready. Cleanup
walks it backward. Independent nodes have no application-defined ordering. A
factory and its `Start` method are one dispatch: once a factory succeeds, its
`Start` method still runs even when the operation context is canceled while the
factory is returning.

On the first startup failure, no later node is dispatched and every transferred
cleanup obligation remains available to the caller. The runtime never performs
an implicit rollback. A no-error factory can still panic, and a managed
resource's `Start` method can still fail; these callbacks are reported as node
failures with their cleanup obligations preserved when a value was transferred.

For a push pipeline, an ordering requirement must be represented by a typed
input such as a ready sink or managed connection. The graph cannot infer a
message topology from a closure body. A source depending on a processor that
depends on a sink gives the required startup and reverse-shutdown edges:
source ingress stops first, the processor drains, and the sink drains last.

## Lifecycle and cleanup

An owned factory transfers ownership only after it returns a successful value. A
nonzero value plus an error transfers nothing and invokes no lifecycle methods;
the factory owns cleanup of its partial acquisition. Once construction
succeeds, the runtime records the cleanup obligation before invoking `Start`.
A `Start` method error, panic, or `Goexit` therefore still leaves the
constructed resource for application cleanup.

`Stop` runs only for successfully constructed owned nodes, including nodes
whose `Start` method failed. A dependency remains available until every
constructed dependent has finished stopping. A successful stop is final for
that node. A failed stop leaves the node pending, retains its dependencies, and
is retried once on the next caller `Stop`; the library does not run a hidden
retry loop or infer completion from an error value. `Stop` and close methods
must be idempotent and retry-safe, including after partial failure. The
application adapts third-party APIs that do not meet that contract.

Pure nodes have no callback but still participate in dependency completion. A
failed stop leaves its node and dependencies retained, so those dependencies
are skipped until the dependent succeeds on a later `Stop`; unrelated branches
continue cleanup in reverse dependency order. Every user callback is isolated
and joined before the next node or operation proceeds, so at most one callback
is active and no detached goroutine may mutate runtime state afterward. When a
shutdown context is canceled, eligible pure nodes may still release their
stored cells because that work cannot invoke user code. Owned callbacks that
were not dispatched remain pending. If all cleanup has completed, late
cancellation does not resurrect it and `Stop` returns nil.

## Context, errors, and limits

The caller supplies independent contexts to `Start` and `Stop`. The library
passes the exact context to context-taking constructors and lifecycle methods.
It checks cancellation before dispatch, prevents new dispatch after
cancellation, waits for the current callback, and returns pending cleanup to
the caller. It does not create rollback contexts, signal handlers, timeouts,
background cleanup, or a `Run` helper. Nil contexts are rejected before state
changes. A non-nil already-canceled startup context consumes the one startup
attempt without invoking factories.

The runtime is one-shot: it cannot be restarted, and overlapping operations
are rejected. `Runtime.Value` is a typed bootstrap read available only after a
successful start and while the runtime is running. Callers must stop using a
borrowed value before calling `Stop`. Stop after incomplete cleanup returns the
pending-cleanup sentinel together with contextual node errors and, when
applicable, the caller's context error. Stored values and cleanup state remain
private.

User errors are retained as causes without invoking their formatting, `Is`,
`As`, or unwrap methods during scheduling and recovery. Panics and `Goexit` are
converted to contextual node failures while preserving cleanup obligations.
Startup returns the first node failure. Cleanup failures are reported in stable
node ID order, followed by the pending-cleanup sentinel and the operation
context error when applicable.

The runtime traverses the finite reachable graph iteratively with O(V+E)
storage and work. Execution uses the stored order without runtime scheduling
queues or dependency counters. One user callback is active at a time. A
callback that ignores cancellation can determine operation latency; queue
admission, message limits, worker shutdown, and provider-specific timeouts
belong to the application owner at the resource boundary.

Reflection-based registration, global type registries, and runtime resolvers
are excluded because they move constructor validation and ownership away from
ordinary Go calls. Consumer code generation is also excluded from the core:
generated typed wrappers would add a build step without strengthening the
constructor contract, while generated reflection would weaken it. Direct
package functions and typed fixed-arity wrappers provide the needed API without
dynamic registration. Separate startup and shutdown DAGs are excluded because
they allow lifetime ordering to diverge from construction ordering. Separate
managed method families are excluded because they duplicate constructor
signatures without proving resource freshness. A single context-and-error
constructor shape would force adapters around ordinary named constructors.

Parallel execution is excluded because it would overlap slow independent
callbacks and introduce scheduling and concurrent failure handling without a
demonstrated latency requirement. Serial execution keeps ownership and failure
sequencing explicit.

Ordering-only `After` edges are also excluded. When ordering is real, compose a
typed readiness or connection capability so construction and lifetime share one
edge. Hidden ordering metadata would create a second source of truth.
