# Component fresh rewrite plan

Status: implemented. This file preserves the rewrite decisions and review history.
[ARCHITECTURE.md](ARCHITECTURE.md) is authoritative for the current architecture;
[README.md](README.md) and the compiled examples describe current usage.

## Outcome and constraints

Build a Go 1.27 library that records ordinary, typed Go construction once and
derives resource startup and shutdown order from the same inputs. Keep
application constructors, structs, interfaces, closures, and methods independent
of the library. References belong in composition code; application objects receive
ordinary values.

The caller supplies independent contexts to `Start` and `Stop`, including all
cancellation and timeout policy. No signal handling, timeout options, background
shutdown, implicit rollback context, or application `Run` helper.

This is a breaking replacement. Remove the registry, global type bindings,
reflection-based constructor invocation, manual duplicate dependency lists,
mandatory `Configure`, and compatibility paths. Preserve the useful graph and
cleanup reasoning, not the existing internal shapes.

Choose ordinary `go build` without consumer code generation. Generic methods
make the typed API concise; they do not provide overloads, heterogeneous
parameter packs, or covariance between `Ref[*Concrete]` and `Ref[Interface]`.

## Public API

Names and signatures below record the implemented replacement design. All generic methods have concrete receivers, as Go 1.27 requires.

```go
// The phantom field prevents explicit conversion between different Ref types.
// It preserves comparability even when T is a slice, map, or function.
type Ref[T any] struct {
    _ [0]*T
    node *definition
}
type Root interface { /* sealed, implemented by Ref[T] */ }

// Value is borrowed. Factories accept zero or one lifecycle declaration.
func Value[T any](value T) Ref[T]
func Provide[T any](create func() T, ownership ...Ownership[T]) Ref[T]
func TryProvide[T any](create func() (T, error), ownership ...Ownership[T]) Ref[T]
func ProvideContext[T any](create func(context.Context) (T, error), ownership ...Ownership[T]) Ref[T]

func (r Ref[A]) Map[B any](create func(A) B, ownership ...Ownership[B]) Ref[B]
func (r Ref[A]) TryMap[B any](create func(A) (B, error), ownership ...Ownership[B]) Ref[B]
func (r Ref[A]) MapContext[B any](create func(context.Context, A) (B, error), ownership ...Ownership[B]) Ref[B]
func (r Ref[A]) With[B any](other Ref[B]) Inputs2[A, B]

type Inputs2[A, B any] struct { /* private refs, not application values */ }
func (in Inputs2[A, B]) Map[T any](create func(A, B) T, ownership ...Ownership[T]) Ref[T]
func (in Inputs2[A, B]) TryMap[T any](create func(A, B) (T, error), ownership ...Ownership[T]) Ref[T]
func (in Inputs2[A, B]) MapContext[T any](create func(context.Context, A, B) (T, error), ownership ...Ownership[T]) Ref[T]
func (in Inputs2[A, B]) With[C any](other Ref[C]) Inputs3[A, B, C]

// Inputs3 and Inputs4 have all three analogous construction methods.
// Inputs3.With creates Inputs4. Inputs4 has no With method.

type Lifecycle interface {
    Start(context.Context) error
    Stop(context.Context) error
}

type Ownership[T any] struct {
    Name string // Optional diagnostic label; private fields enforce construction.
}
func Managed[T Lifecycle]() Ownership[T]

type RuntimeOptions struct {
    Parallelism int // zero defaults to 1; negative is invalid
}

func New(options RuntimeOptions, roots ...Root) (*Runtime, error)
func (rt *Runtime) Start(ctx context.Context) error
func (rt *Runtime) Stop(ctx context.Context) error
func (rt *Runtime) Value[T any](ref Ref[T]) (T, error)
```

### Composition rules

- `Value` supplies a borrowed value. It does not acquire, close, or take ownership
  of it. A function passed to `Value` is a value, not an implicitly invoked provider.
- All factories run during `Start`, not declaration or `New`. Context-taking
  factories receive exactly the caller's startup context. `ProvideContext` and
  `MapContext` support I/O that produces an unmanaged value without a fake stop hook.
  Context-taking factories without an error result use a closure that calls the
  original constructor and returns its result followed by nil. Keep three
  supported function shapes; a fourth family adds surface without removing a type-safety limitation.
- Without an ownership argument, a factory produces an unmanaged value, even if
  its result implements Lifecycle. Supplying `Managed[T]()` declares ownership;
  the compiler requires T to implement Start and Stop with context-only methods.
  An owner needing no startup work implements Start by returning nil. `Value`
  remains borrowed and cannot accept ownership. External APIs use small owner
  adapters with ordinary lifecycle methods.
- A managed factory must create a distinct owned resource, not return an alias
  of an input or another owned object. Projections remain unmanaged and retain
  their source's dependency edge. Freshness is an application contract that Go's
  type system cannot prove, regardless of the construction method's name.
- Multiple ownership arguments, zero Ownership values, nil constructors, and
  invalid references are declaration errors rejected by New before constructors
  run. Managed is the only public constructor for a valid Ownership value.
- Managed construction transfers ownership only on success. Register the stop
  obligation before invoking its start hook. A constructor returning nonzero T
  plus an error transfers nothing: discard T, invoke no hooks, and leave partial
  cleanup to the factory. Every factory, including unmanaged factories, owns
  cleanup of partial acquisitions. The same local-ownership rule applies if it panics or
  calls Goexit before returning successfully. APIs needing cleanup after failed
  startup should construct an inert object and use its start/stop hooks.
- Typed nils are valid ordinary values (for example optional options). A managed
  constructor returning nil successfully is an error; do not invoke its hooks.
- `With` stores refs in parameter order without creating a node. Repeated inputs
  remain repeated arguments but become one graph edge. Each constructed ref is a
  distinct identity, even when constructors, names, and output types match.
- Names are diagnostics only. Empty names use stable per-runtime IDs and declared
  Go types. Do not derive semantic identity from function names or pointers.
- Definitions and their dependency lists are immutable. Build new definitions
  only from existing refs; there are no forward declarations, setters, or
  order-only `After` edges. This makes the public construction graph acyclic.
- Support one through four constructor inputs directly. Larger constructors use
  an ordinary typed dependency struct composed from smaller groups and a closure
  making the original call. Do not add a variadic/reflection escape hatch.
- Interface adaptation uses normal assignment inside a closure; reuse a mapped
  interface view when useful. Do not provide an unchecked `As[I]` conversion.
- `Runtime.Value` is a typed bootstrap operation, available only after a
  successful `Start` and before any `Stop`. Reject unknown refs and all other
  runtime states. A constructor cannot use it as a resolver during startup.

### Usability examples

```go
// Existing named constructor with no error result (Upspin).
cfg := component.Value[upspin.Config](existingConfig)
clientRef := cfg.Map(client.New)

// Multiple ordinary inputs; no repeated dependency declarations.
serviceRef := database.With(logger).Map(NewService)

// Interface assignment is checked at this ordinary Go call.
handlerRef := database.Map(func(db *Database) *Handler {
    return NewHandler(db) // NewHandler accepts a Store interface.
})

// More than four inputs: compose a normal struct, then call the original API.
storage := store.With(cache).With(logger).Map(func(s Store, c Cache, l Logger) StorageInputs {
    return StorageInputs{Store: s, Cache: c, Logger: l}
})
largeService := storage.With(httpConfig).With(jobConfig).Map(func(s StorageInputs, h HTTPConfig, j JobConfig) *Service {
    return NewLargeService(s.Store, s.Cache, s.Logger, h, j)
})

// Existing variadic API and struct literal (Go kit / net/http).
transportRef := endpointRef.Map(func(ep endpoint.Endpoint) *kithttp.Server {
    return kithttp.NewServer(ep, decode, encode, options...)
})
httpRef := transportRef.Map(func(h *kithttp.Server) *http.Server {
    return &http.Server{Addr: address, Handler: h}
})
// httpRef is an unmanaged server value here. A serving owner must own the
// listener, serving goroutine, readiness handshake, and graceful shutdown.

// An application owner wraps Go CDK acquisition and idempotent cleanup.
// openBucketOwner calls blob.OpenBucket and returns a *BucketOwner implementing
// Start(ctx) and Stop(ctx). The raw bucket API remains unchanged.
bucketRef := bucketURL.MapContext(openBucketOwner, component.Managed[*BucketOwner]())

// Existing constructor and ordinary receiver methods.
sourceRef := sinkRef.Map(NewSource, component.Managed[*Source]())

rt, err := component.New(component.RuntimeOptions{}, sourceRef)
if err != nil { return err }
startErr := rt.Start(startCtx)
// The application chooses when to stop; it also calls Stop after failed Start.
stopErr := rt.Stop(stopCtx)
return errors.Join(startErr, stopErr)
```

These are independent composition examples, not one complete application. The
acceptance examples must make the managed pipeline and HTTP lifecycle complete.

## Graph, ownership, and runtime

### Definition and instance separation

`Ref[T]` describes construction. A runtime owns one instance per reachable
definition. `New` traverses only the roots' dependency closure, deduplicates shared
nodes, validates it without user execution, and compiles immutable adjacency.
Unused definitions do not run. Repeated roots are harmless.

The same definitions can create independent runtimes. Factories must return fresh
owned resources per runtime; borrowed `Value` inputs may intentionally be shared.
Do not attempt process-wide pointer/resource deduplication: arbitrary values,
closures, and hidden resources do not have a reliable general identity. Reuse the
same ref instead of declaring the same owned instance twice. Aliases/projections
are unmanaged values whose lifetime remains linked to their source owner.

Use closed generic adapters to create internal erased calls. User code receives
typed arguments, not an internal resolver or `[]any`. No `reflect.Call`, global
type registry, assignability search, or global ordinal counter. Reflection is
permitted only for boundary nil checks and type diagnostics.

Store each result in a typed `valueCell[T]` erased behind an internal interface,
rather than asserting a directly boxed T from `any`. This preserves valid nil
interface values. Each node has one authoritative instance cell.

Assign stable per-runtime IDs in root/input traversal order. Use iterative graph
traversal, forward and reverse adjacency, and dependency counts. Preserve pure
nodes in the graph so resource lifetimes flow through closures and adapters.
Deduplicate repeated input edges in both adjacency and dependency counts while
preserving repeated constructor arguments. Avoid recursive processing on deep
graphs. A residual cycle is an internal invalid-graph error, not an invitation to
add forward references.

### Startup

Each node is constructed only after all inputs are fully ready. After successful
managed construction, record its cleanup obligation, then invoke Start(ctx).
The node becomes ready only after Start succeeds. Unmanaged nodes become ready
when construction succeeds. There is no global preparation barrier; ordinary
unmanaged values need no lifecycle interface.

Use a ready queue ordered by stable node ID with at most `Parallelism` user calls
in flight. Default to serial execution. Dispatch dependent work as prerequisites
finish, without waiting for unrelated frontier members. On the first observed
failure, stop dispatching new work, await in-flight calls, retain every transferred
cleanup obligation, and return errors in stable node order. Do not invoke Stop.

The caller's context is passed unchanged to context-taking constructors and
hooks. Check cancellation before dispatch and before committing startup success.
Dispatch commits to the entire node invocation: a successful managed factory
still calls Start(ctx) if cancellation or a sibling failure arrives
while the factory is running. It receives the same, possibly canceled context.
Preserve the successful value and cleanup obligation if that hook errors, panics,
or calls Goexit. Cancellation prevents further node dispatch, not this handoff.
Await in-flight user calls even after cancellation; no detached callback can
continue mutating an apparently stopped runtime. Callback cooperation bounds
elapsed time; Go cannot forcibly terminate a blocked function.

### Shutdown and idempotent cleanup

An edge means: the dependency must be ready before its dependent starts and must
remain available until that dependent has finished stopping. Successful Stop
means every owned worker has exited and cannot use a dependency or emit again.
Managed owners define their own queue admission and drain/abort policy.

Only successfully constructed nodes participate in teardown, including nodes
whose start hook failed. Pure nodes complete teardown without a callback, but
only after their constructed dependents are complete. A managed dependency is
eligible only when all of its constructed dependents are complete.

Stop and close hooks MUST tolerate repeated calls, including after partial
failure. The application/resource owner supplies the necessary idempotence.
Nil means cleanup succeeded and the node is complete. An error does not tell the
library whether anything closed; leave the node pending and retain its required
dependencies. Do not infer completion from error types or third-party method
names. Do not expose a result type that lets an error mark a node complete.

Adapting an existing Close method is the application's responsibility when its
native repeated-call or partial-failure behavior does not satisfy this contract.
The library does not swallow already-closed errors, synthesize idempotence, or
make a context-free Close cancellable. The Go CDK example intentionally names an
application adapter rather than advertising raw Bucket.Close as sufficient.

Shutdown uses the same concurrency bound and advances unrelated eligible
branches after a failure. A pending node is tried once per Stop call, not in a
hidden loop. Panic or Goexit in a stop callback leaves cleanup pending. Nodes
whose stop hook succeeded are never retried.

Check the shutdown context before dispatch. On cancellation, retain undispatched
work, await in-flight calls, process their actual outcomes, and return the context
error when cancellation leaves work pending. If no cleanup remains, return the
actual callback errors and mark stopped; cancellation alone does not resurrect
completed cleanup. The library creates no substitute context.

### Runtime state and errors

Reject nil contexts before any state transition for both operations. An already
canceled non-nil Start context consumes the one startup attempt and invokes no
factories. A final cancellation check may fail Start after all nodes became ready;
the caller must still Stop to discharge transferred cleanup obligations.

The runtime is one-shot: idle -> starting -> running or cleanup-pending/stopped;
running/cleanup-pending -> stopping -> cleanup-pending or stopped. Stop before
Start consumes the runtime without invoking providers. Repeated Stop after
terminal cleanup is a no-op. Restart is unsupported. Concurrent Start/Stop calls
return a busy error; Stop does not cancel an in-flight Start.

Runtime copies must share one identity and state; zero or nil Runtime is invalid.
Values and cleanup state are private. Release stored values after their terminal
cleanup; values already returned to application code remain borrowed references
that the caller must stop using before shutdown.

Every user invocation runs in an owned goroutine with exactly one deferred result
publication. Recover panics and detect Goexit through deferred completion
publication, producing contextual node errors. Join every invocation before its
operation returns. Do not call user Error, String, or Format
methods in scheduling or recovery, nor user Is, As, or Unwrap methods. Store error
causes and render them only when the caller asks for error text. Preserve errors.Is/As and panic stacks. Do not
retry construction, startup, or cleanup automatically.

Expose stable sentinels for invalid definitions/references, invalid runtime or
options, busy operations, already-started runtimes, unavailable values, pending
cleanup, panic, and aborted invocation. Use one contextual
node error type with node ID/name, phase, and cause; avoid one error type per helper.
Sort concurrent node failures by stable node ID, then append the operation's own
context error at most once. Do not inspect opaque node causes to deduplicate it;
a node cause may independently wrap the same context error. Empty graphs are
valid and follow the same one-shot state rules.

## Guarantees and integration limits

The Go compiler checks constructor arity, declared input/output types, closure
bodies, field assignments, and the managed owner method contract. Immutable construction prevents public
dependency cycles. Invalid zero refs, nil functions, lifecycle option combinations,
and resource failures remain runtime validation; Go has no non-null or required
struct-field types. Same-type semantic roles require domain types when selecting
the wrong instance must itself be a compiler error.

Declare managed dependencies as typed inputs. Ordinary immutable configuration
may be captured. The library cannot discover arbitrary closure captures, global
lookups, dynamic registries, or message topology from a function's body. Never
claim otherwise. For push pipelines, producers receive downstream sinks or a
managed connection that owns subscription readiness. If an ordering requirement
has no existing value dependency, inject a typed readiness/connection capability
that represents it; hidden topology does not gain inferred edges. Construction and resource
ordering then agree; inverse teardown stops ingress before draining downstream.

Reuse Upspin client.New directly. Its lazy global bind cache is outside the graph;
full server management needs an ownership adapter/upstream change. Support Go
kit's constructors through small ordinary closures. Support Go CDK acquisition
through MapContext plus explicit cleanup. A blocking Run/Serve needs an adapter that
establishes readiness, interrupts work, and joins it; do not auto-detect lifecycle
from method names or treat a launched goroutine as proof of readiness.

Rejected alternatives: reflection registration loses compile-time checks; a
scoped resolver discovers edges during user execution and repeats error handling;
consumer code generation adds a build step without evidence the small typed API
is inadequate; requiring lifecycle methods on unmanaged values pollutes ordinary
types; separate
start/stop DAGs weaken the lifetime invariant; decorating an existing ref with a
new owning alias splits ownership. Hooks attach at creation of the owning node.
Separate `Manage`, `TryManage`, and `Open` families were rejected in the API
revision: they duplicate all three signatures at every arity without enforcing
resource freshness. A lifecycle argument declares ownership explicitly. Reducing
to one context/error signature was also rejected because it would require adapter
closures for ordinary named constructors that the three forms accept directly.

## Implementation sequence and acceptance

All five implementation steps are complete. The original acceptance criteria
are retained below as historical context; current contracts live in ARCHITECTURE.md.

1. **Completed: prove the public signatures.** Compile a scratch package on Go 1.27 exercising
   generic methods on Ref and Inputs2/3/4, result inference, named functions,
   defined function types, method expressions, ordinary interface adapters, and
   error/no-error/context-taking variants. Compile the full four-input chain.
   Exercise every construction form both with and without explicit ownership.
   Use ordinary package tests and compiled examples for valid typed consumers.
   Do not add subprocess builds or intentionally invalid source fixtures to test
   the Go compiler. Incorrect arity and types remain compiler-enforced contracts.
   Completion: all shown API examples are expressible without reflection or
   consumer generation; no unsupported generic-instantiation cycle.
2. **Completed: implement definitions and typed adapters.** Add refs, input groups, options,
   lifecycle hooks, and pure root validation. Keep family code mechanical and
   hand-written for the four supported arities. Completion: declarations execute
   no user code, invalid graphs are rejected before effects, identities are
   immutable, shared refs materialize once per runtime.
3. **Completed: implement the runtime owner.** Add instance storage, bounded ready queues,
   per-node construction/readiness, independent caller-context operations, cleanup
   retries, one-shot/busy states, typed Value access, and safe invocation/error
   packaging. Completion: startup/shutdown behavior and cancellation/partial
   failure satisfy the tests below, with no owned invocation left running after
   an operation returns.
4. **Completed: replace the existing implementation.** Delete Registry/Register/Bind/Provide
   reflection machinery, old Node/Lifecycle/Configure APIs, global initialization
   locks/ordinals, old frontier barriers, and obsolete tests/sentinels. Migrate
   examples and useful graph/cleanup tests to the new behavioral contracts.
   Completion: one construction path and one lifecycle state owner remain; no
   compatibility wrappers or alternative resolution engine.
5. **Completed: document and verify.** Make README the quickstart and ARCHITECTURE.md the
   canonical current architecture entry point when implementation lands; link to
   the accepted decisions here without duplicating their authority indefinitely.
   Convert this plan to completed history after current architecture is recorded.
   Run the repository's tidy-diff, Staticcheck, vet, and race/shuffle checks.
   Completion: documented examples compile, required checks pass, and the new
   implementation's changes/limitations are reported.

Acceptance tests must include:

- Compiled tests/examples for named no-error/error constructors, generic methods,
  closures, structs, same-type instances, and typed interface adaptation. Check
  comparable refs for slice,
  map, and function T, nil interface results, closure outputs, Value(function),
  and adaptation of context-taking constructors without error results.
- Nil/zero refs and functions, invalid options, multiple ownership arguments,
  zero ownership declarations, empty graphs, repeated roots and
  arguments, typed-nil ordinary values, managed nil results, and no constructors
  before complete validation. Constructor results are not compared to their own
  setup; assert externally observed behavior.
- Root-only reachability; shared owner built/started/stopped once; pure adapters
  preserve lifetime edges; independent runtimes create independent owned values.
- A deterministic source -> processor -> sink pipeline: first message reaches a
  ready sink, startup waits for subscription readiness, stop joins upstream before
  downstream drain, and the last accepted message is handled before completion.
- Realistic HTTP serving with caller-created startup/shutdown contexts, listener
  readiness, serving goroutine ownership, graceful draining, failed startup, and
  partial shutdown. Keep external project patterns as test fixtures without
  requiring cloud credentials or large external module dependencies.
- Successful acquisition followed by later failure; start hook failure after
  construction; failed constructor's local ownership; caller Stop after failure;
  nonzero T plus constructor error invokes no hooks; Start panic/Goexit preserves
  successfully constructed ownership; no internal rollback or context substitution.
- A failed Stop leaves its node pending and retains dependencies; it retries once
  only on the next caller Stop; successful stops never repeat; unrelated branches progress;
  mixed/shared branches and partially released resources; panic/Goexit is pending.
- Callback bounds at serial and configured parallelism; fast dependent advances
  without an unrelated frontier barrier; cancellation prevents new dispatch but
  waits for in-flight calls; callbacks receive exactly the supplied context.
  Cancellation or sibling failure during a successful factory still invokes its
  Start hook. Nil contexts do not change state; already-canceled Start consumes
  the attempt. Verify final-start cancellation and late-stop cancellation.
- One-shot/overlap/copy behavior and Value access before start, during operations,
  after failure/stop, for foreign refs, and for a valid running instance.
- Callback/constructor panics, Goexit, and errors whose formatting methods panic,
  Goexit, or block, and causes with hostile Is/As/Unwrap methods: internal result publication never formats them or strands the
  scheduler. Failure causes remain inspectable with errors.Is/As.
- Deep chain, diamond, and wide graphs with iterative traversal. Benchmark graph
  creation separately from user I/O; bound concurrent callbacks rather than
  pursuing an unsupported performance target. Reuse current 1,000-node graph
  measurements only as a baseline, not a compatibility requirement.

## Evidence

- [Go 1.27 generic methods](https://go.dev/doc/go1.27#language).
- [Upspin client constructor](https://github.com/upspin/upspin/blob/master/client/client.go)
  and [global binding cache](https://github.com/upspin/upspin/blob/334f107fe3d98225d7adfbb35b74e066fbca9875/bind/bind.go).
- [Go kit HTTP construction](https://github.com/go-kit/kit/blob/master/transport/http/server.go).
- [Go CDK bucket acquisition and Close](https://github.com/google/go-cloud/blob/master/blob/blob.go).
- [Go CDK generated composition](https://github.com/google/go-cloud/blob/master/samples/guestbook/wire_gen.go)
  and [Wire's same-type limitation](https://github.com/google/wire/blob/main/docs/faq.md).
- [oklog/run interrupt and join](https://github.com/oklog/run/blob/main/group.go).

## Review record

Review history before the API reduction:

First pass: three independent adversarial reviewers covered API/type safety,
lifecycle/concurrency, and integration/usability. Material corrections separate
unmanaged mapping from ownership, add context-taking unmanaged factories, prevent
cross-type Ref conversion, preserve nil interface values, define cancellation at
the factory/Start boundary, and specify partial-construction ownership and compile
fixtures. Cleanup remains application-idempotent: errors always leave it pending.

Second pass: all three reviewers reported no remaining material objections.

- API/type safety: a scratch Go 1.27 module compiled all six construction methods,
  generic receiver families, result inference, explicit method type arguments,
  interface adapters, and the full four-input chain. Comparable slice/map/function
  refs compiled; explicit `Ref[int](Ref[string]{})` conversion failed with the
  specified phantom field. Removing that field allowed the conversion.
- Lifecycle/concurrency: reviewed complete construction, readiness, cancellation,
  failure, and retry paths. Clarified that every factory owns partial acquisitions
  and that deferred publication detects Goexit rather than recovering it.
- Integration/usability: accepted ordinary constructor/closure integration,
  contextful unmanaged construction, input grouping, and readiness capabilities.
  Deliberately retain closure adaptation for context-taking no-error constructors
  instead of adding a fourth signature family; include a compiled example.

Subsequent user-requested API reduction: replace the six construction forms with
three forms accepting zero or one lifecycle argument. Update signatures, examples,
validation rules, and acceptance cases together. This supersedes the reviewers'
preference for separate managed method names; that naming split did not enforce
fresh ownership. Retain construction-time hook attachment, the prohibition on
owning aliases, and all lifecycle/context/cleanup semantics. The reduced API was subsequently reviewed in the implementation pass below.

Subsequent verification direction: the user requested normal tests and compiled
examples instead of subprocess builds or invalid-source fixtures. Historical
compiler probes remain evidence from planning, not a required test harness.

## Implementation review and verification

Three independent subagents reviewed the implemented API/type boundary,
lifecycle/concurrency behavior, and integration/documentation against this plan.
The API and runtime reviews found no material implementation defects. Integration
review identified a weak startup-order assertion in the pipeline test; upstream
Start hooks now require downstream readiness before launching workers. Incorrect
test expectations, an uninitialized HTTP test result channel, and documentation
about Value availability were corrected during verification.

The replacement removes Registry/Register/Bind, reflective constructor calls,
mandatory Configure, global registration state, and the old frontier scheduler.
Typed adapters feed a single runtime state owner. Cleanup errors remain pending;
no completion outcome type or automatic retry policy was introduced.

Verification completed with Go 1.27:

- Ordinary tests and executable examples, including all three constructor forms
  at arities zero through four with and without explicit ownership.
- Ownership, failure, panic/Goexit, context, retry, concurrency, nil-value, and
  graph-shape tests; real local HTTP serving and a managed push pipeline.
- Repository tidy-diff, Staticcheck, vet, and race/shuffle checks.
- Graph-only 1,000-node compilation benchmarks, separate from user callbacks;
  measurements remain a baseline, not a performance contract.

No subprocess compilation harness or intentionally invalid source fixtures remain.
Application cleanup idempotence, fresh resource ownership, explicit managed
dependencies, and cooperative callbacks remain binding application contracts.

## Context-only owner methods

The subsequent lifecycle UX revision replaces the public generic callback struct
with `Lifecycle` and `Managed[T]()`. The receiver supplies the resource; Start and
Stop take only the caller context. The compiler checks the method contract at the
ownership declaration. Ownership remains explicit, and lifecycle-like methods on
ordinary values never trigger management automatically.

`Ownership[T]` is a typed declaration token with an optional diagnostic Name.
Its private adapter can be created only by Managed; New rejects zero tokens.
This retains direct named constructor calls without exposing callbacks taking
both T and context or adding a separate managed constructor family. The runtime
state machine, ownership transfer point, and cleanup retry semantics are unchanged.
This decision supersedes the generic hook struct discussed in the earlier reviews.

The ownership API received a focused subagent review with no material regression
found. Migrated tests and examples pass, including unmanaged lifecycle-capable
values and all constructor forms with explicit ownership. Repository tidy-diff,
Staticcheck, vet, and race/shuffle checks pass for this revision.
