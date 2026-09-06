# Component architecture

This document is the canonical entry point for the current architecture. It
records the implementation decisions that must remain true as the code evolves.

## Capability and ownership

`component` owns two capabilities:

1. It records an immutable, typed construction graph and derives the ordering
   needed for startup and shutdown.
2. It owns one runtime instance of each reachable definition and runs
   construction, readiness, and cleanup in deterministic serial order.

Application code owns resource policy. It defines the resource types,
constructors, readiness handshakes, queue admission, drain or abort behavior,
and idempotent cleanup. The library never discovers lifecycle methods by name,
resolves values dynamically, or infers hidden closure captures.

`Ref[T]` is a composition value, not an application dependency. A factory
receives `T` directly. `Value` records a borrowed value. `Provide`, `TryProvide`,
`ProvideContext`, and their `Map` counterparts record an unmanaged value unless
one `Ownership[T]` from `Managed[T]` is supplied. `T` must implement the
`Lifecycle` interface with `Start(context.Context) error` and
`Stop(context.Context) error`. Ownership is attached at node creation, so the
factory that creates an object is the only owner that can declare its stop
policy. Decorating an existing ref with an owning alias is not supported.

References for different value types cannot be converted into one another.
A phantom pointer array to the named generic `valueCell[T]` preserves this
boundary even for anonymous structs differing only in tags, while keeping
references comparable for slice, map, and function values. A direct `*T`
marker would permit tag-only conversions and break typed runtime retrieval.

`Managed[T]` requires `Start` even for an inert resource; an inert method can
return nil. `Stop` owns all retry and partial-cleanup behavior. An unmanaged
value remains unmanaged even if its type implements `Lifecycle`.

An owned factory must return a fresh resource for each runtime and must not
return an input or another owned resource. This freshness rule is an
application invariant: arbitrary Go values and aliases cannot be proven fresh
by the type system. Existing shared objects remain borrowed through `Value`.

## Graph and execution

Definitions are immutable and are reachable only through the roots passed to
`New`. `With` groups one through four typed inputs without creating a node; it
preserves argument order and deduplicates repeated graph edges. A reference is
one identity even when its constructor or output type matches another
reference. Larger constructors use ordinary typed grouping structs and a
closure.

`New` validates all reachable definitions before user code runs. It rejects
zero or invalid refs, nil functions, invalid ownership declarations, and
cycles. The runtime then materializes one instance per reachable definition. Shared
refs are constructed, started, and stopped once per runtime; independent
runtimes have independent owned values. Pure mapping nodes remain in the graph
so that a dependent's lifetime keeps its source alive.

`New` computes a deterministic topological order once. Startup walks it
forward, so each node runs only after all of its inputs are ready. Cleanup
walks it backward. Independent nodes have no application-defined ordering. A factory and its `Start` method are one dispatch: once a
factory succeeds, its `Start` method still runs even when the
operation context is canceled while the factory is returning. On the first
failure, no later node is dispatched and every transferred cleanup obligation
remains available to the caller. The runtime never performs an implicit
rollback. A successful factory and its cleanup obligation therefore survive a
`Start` method error, panic, or `Goexit`.

For a push pipeline, an ordering requirement must be represented by a typed
input such as a ready sink or managed connection. The graph cannot infer a
message topology from a closure body. A source depending on a processor that
depends on a sink gives the required startup and reverse-shutdown edges:
source ingress stops first, the processor drains, and the sink drains last.

## Lifecycle and cleanup

An owned factory transfers ownership only after it returns a successful value.
A nonzero value plus an error transfers nothing and invokes no lifecycle
methods; the factory owns cleanup of any partial acquisition. Once construction
succeeds, the runtime records the cleanup obligation before invoking `Start`. A `Start`
method error, panic, or `Goexit` therefore still leaves the constructed resource
for application cleanup.

`Stop` runs only for successfully constructed owned nodes, including nodes
whose `Start` method failed. A dependency remains available until every
constructed dependent has finished stopping. A successful stop is final for
that node. A failed stop leaves the node pending, retains its dependencies, and
is retried once on the next caller `Stop`; the library does not run a hidden
retry loop or infer completion from an error value. `Stop` and close methods must
be idempotent and retry-safe, including after partial failure. The application
adapts third-party APIs that do not meet that contract.

Pure nodes have no callback but still participate in dependency completion. A
failed stop leaves its node and dependencies retained, so those dependencies
are skipped until the dependent succeeds on a later `Stop`; unrelated branches
continue cleanup in reverse dependency order. Every user callback is isolated
and joined before the next node or operation proceeds, so at most one callback
is active and no detached goroutine may mutate runtime state afterward. When a
shutdown context is canceled, eligible pure nodes may still release their
stored cells because that work cannot invoke user code. Owned callbacks that
were not dispatched remain pending. If all cleanup has already completed, late
cancellation does not resurrect it and `Stop` returns nil.

## Context, errors, and limits

The caller supplies independent contexts to `Start` and `Stop`. The library
passes the exact context to context-taking constructors and lifecycle methods.
It checks cancellation before dispatch, prevents new dispatch after
cancellation, waits for the current callback, and returns pending cleanup to
the caller. It does not create rollback contexts, signal handlers, timeouts, background cleanup, or
a `Run` helper. Nil contexts are rejected before state changes. A non-nil
already-canceled startup context consumes the one startup attempt without
invoking factories.

The runtime is one-shot: it cannot be restarted, and overlapping operations
are rejected. `Runtime.Value` is a typed bootstrap read available only after a
successful start and while the runtime is running. Callers must stop using a
borrowed value before calling `Stop`. Stop after incomplete cleanup returns the
pending-cleanup sentinel together with contextual node errors and, when
applicable, the caller's context error. Stored values and cleanup state remain
private.

User errors are retained as causes without invoking their formatting,
`Is`, `As`, or unwrap methods during scheduling and recovery. Panics and
`Goexit` are converted to contextual node failures while preserving cleanup
obligations. Startup returns the first node failure. Cleanup failures are
reported in stable node ID order, followed by the pending-cleanup sentinel and
the operation context error when applicable.

The runtime traverses the finite reachable graph iteratively with O(V+E)
storage and work. Execution uses the stored order without runtime scheduling
queues or dependency counters. One user callback is active at a time. It does not promise a throughput target
or force callbacks to finish: a user callback that ignores cancellation can
determine operation latency. Queue admission, message limits, worker shutdown,
and provider-specific timeouts belong to the application owner at the resource
boundary.

## Deliberate exclusions

Parallel execution would overlap slow independent callbacks, but introduces
scheduling and concurrent failure handling without a demonstrated latency
requirement. Serial execution keeps ownership and failure sequencing explicit.

Reflection-based registration, global type registries, and runtime resolvers
would move constructor validation and dependency ownership away from ordinary
Go calls. Consumer code generation would add a build step without improving
the typed constructor contract. Separate startup and shutdown DAGs would allow
lifetime ordering to diverge from construction ordering. Separate managed
method families would duplicate each constructor signature without proving
resource freshness. A single context/error constructor shape would force
adapters around ordinary named constructors. None is part of the target
architecture.

Ordering-only `After` edges are also excluded. When ordering is real, compose a
typed readiness or connection capability so construction and lifetime share one
edge. Hidden ordering metadata would create a second source of truth.
