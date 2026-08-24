package component

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type providerCapability interface {
	Identity() string
}

type alternateProviderCapability interface {
	Identity() string
}

type lifecycleProviderCapability interface {
	Lifecycle
	providerCapability
}

type providerLifecycle struct {
	LifecycleFuncs
	identity string
}

func (l *providerLifecycle) Identity() string { return l.identity }

type alternateProviderLifecycle struct {
	LifecycleFuncs
	identity string
}

func (l *alternateProviderLifecycle) Identity() string { return l.identity }

type providerService struct {
	LifecycleFuncs
	first providerCapability
}

type providerConfig struct {
	LifecycleFuncs
}

type tunnelCapability interface {
	Mode() string
}

type kernelTunnelOwner struct {
	LifecycleFuncs
}

func (*kernelTunnelOwner) Mode() string { return "kernel" }

type userspaceTunnelOwner struct {
	LifecycleFuncs
}

func (*userspaceTunnelOwner) Mode() string { return "userspace" }

type concreteProviderError struct{}

func (concreteProviderError) Error() string { return "concrete error" }

type goexitConstructionError struct{}

func (goexitConstructionError) Error() string {
	runtime.Goexit()
	return "unreachable"
}

func TestProvideResolvesUniqueInterfaceAndOrdersLifecycle(t *testing.T) {
	t.Parallel()

	var events []string
	logger := &providerLifecycle{
		identity: "logger",
		LifecycleFuncs: LifecycleFuncs{
			OnStart: func(context.Context) error {
				events = append(events, "start logger")
				return nil
			},
			OnStop: func(context.Context) error {
				events = append(events, "stop logger")
				return nil
			},
		},
	}
	service := &providerService{LifecycleFuncs: LifecycleFuncs{
		OnStart: func(context.Context) error {
			events = append(events, "start service")
			return nil
		},
		OnStop: func(context.Context) error {
			events = append(events, "stop service")
			return nil
		},
	}}

	registry := NewRegistry()
	loggerRef := mustProvide(t, registry, "logger", func() *providerLifecycle {
		return logger
	})
	if loggerRef.String() != "logger" {
		t.Fatalf("logger reference label = %q, want logger", loggerRef)
	}
	mustProvide(t, registry, "service", func(capability providerCapability) *providerService {
		service.first = capability
		return service
	})

	runtime, err := registry.Compile()
	if err != nil {
		t.Fatalf("Compile() failed: %v", err)
	}
	if service.first != logger {
		t.Fatal("service did not receive the uniquely assignable logger")
	}
	if got, want := frontierLabels(runtime), [][]string{{"logger"}, {"service"}}; !equalFrontiers(got, want) {
		t.Fatalf("frontiers = %v, want %v", got, want)
	}
	if err := runtime.Start(t.Context()); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	if err := runtime.Stop(t.Context()); err != nil {
		t.Fatalf("Stop() failed: %v", err)
	}
	if got, want := strings.Join(events, ","), "start logger,start service,stop service,stop logger"; got != want {
		t.Fatalf("lifecycle events = %q, want %q", got, want)
	}
}

func TestCompileAmbiguousInterfaceCanBeRepairedWithBind(t *testing.T) {
	t.Parallel()

	var firstConstructed atomic.Int32
	var secondConstructed atomic.Int32
	var serviceConstructed atomic.Int32
	var firstStarted atomic.Int32
	var secondStarted atomic.Int32
	first := &providerLifecycle{
		identity: "first",
		LifecycleFuncs: LifecycleFuncs{OnStart: func(context.Context) error {
			firstStarted.Add(1)
			return nil
		}},
	}
	second := &alternateProviderLifecycle{
		identity: "second",
		LifecycleFuncs: LifecycleFuncs{OnStart: func(context.Context) error {
			secondStarted.Add(1)
			return nil
		}},
	}

	registry := NewRegistry()
	mustProvide(t, registry, "first", func() *providerLifecycle {
		firstConstructed.Add(1)
		return first
	})
	secondRef := mustProvide(t, registry, "second", func() *alternateProviderLifecycle {
		secondConstructed.Add(1)
		return second
	})
	var injected providerCapability
	mustProvide(t, registry, "service", func(capability providerCapability) *providerService {
		serviceConstructed.Add(1)
		injected = capability
		return &providerService{}
	})

	_, err := registry.Compile()
	if !errors.Is(err, ErrAmbiguousDependency) {
		t.Fatalf("Compile() error = %v, want ErrAmbiguousDependency", err)
	}
	for name, calls := range map[string]int32{
		"first":   firstConstructed.Load(),
		"second":  secondConstructed.Load(),
		"service": serviceConstructed.Load(),
	} {
		if calls != 0 {
			t.Fatalf("%s constructor calls after structural failure = %d, want 0", name, calls)
		}
	}
	if !strings.Contains(err.Error(), "first") || !strings.Contains(err.Error(), "second") {
		t.Fatalf("ambiguity diagnostic %q does not list both candidates", err)
	}

	if err := registry.Bind[providerCapability](secondRef); err != nil {
		t.Fatalf("Bind() failed: %v", err)
	}
	compiled, err := registry.Compile()
	if err != nil {
		t.Fatalf("Compile() after Bind() failed: %v", err)
	}
	if injected != second {
		t.Fatal("binding did not select the second owner")
	}
	if firstConstructed.Load() != 1 || secondConstructed.Load() != 1 || serviceConstructed.Load() != 1 {
		t.Fatalf(
			"constructor calls = first:%d second:%d service:%d, want all 1",
			firstConstructed.Load(),
			secondConstructed.Load(),
			serviceConstructed.Load(),
		)
	}
	if err := compiled.Start(t.Context()); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	if firstStarted.Load() != 1 || secondStarted.Load() != 1 {
		t.Fatalf(
			"binding deactivated an owner: first starts=%d second starts=%d",
			firstStarted.Load(),
			secondStarted.Load(),
		)
	}
}

func TestProvideExactInterfacePrecedesAssignableConcrete(t *testing.T) {
	t.Parallel()

	concrete := &providerLifecycle{identity: "concrete"}
	exact := &providerLifecycle{identity: "exact"}
	registry := NewRegistry()
	mustProvide(t, registry, "concrete", func() *providerLifecycle {
		return concrete
	})
	mustProvide(t, registry, "exact", func() lifecycleProviderCapability {
		return exact
	})
	var injected lifecycleProviderCapability
	mustProvide(t, registry, "service", func(capability lifecycleProviderCapability) *providerService {
		injected = capability
		return &providerService{}
	})

	if _, err := registry.Compile(); err != nil {
		t.Fatalf("Compile() failed: %v", err)
	}
	if injected != exact {
		t.Fatal("exact declared interface did not precede assignable concrete owner")
	}
}

func TestBindOverridesExactInterfaceResolution(t *testing.T) {
	t.Parallel()

	concrete := &providerLifecycle{identity: "concrete"}
	exact := &providerLifecycle{identity: "exact"}
	registry := NewRegistry()
	concreteRef := mustProvide(t, registry, "concrete", func() *providerLifecycle {
		return concrete
	})
	mustProvide(t, registry, "exact", func() lifecycleProviderCapability {
		return exact
	})
	if err := registry.Bind[lifecycleProviderCapability](concreteRef); err != nil {
		t.Fatalf("Bind() failed: %v", err)
	}
	var injected lifecycleProviderCapability
	mustProvide(t, registry, "service", func(capability lifecycleProviderCapability) *providerService {
		injected = capability
		return &providerService{}
	})

	if _, err := registry.Compile(); err != nil {
		t.Fatalf("Compile() failed: %v", err)
	}
	if injected != concrete {
		t.Fatal("explicit binding did not override exact interface resolution")
	}
}

func TestManualRegistrationResolutionUsesDeclaredType(t *testing.T) {
	t.Parallel()

	t.Run("specific type is injectable", func(t *testing.T) {
		t.Parallel()

		logger := &providerLifecycle{identity: "manual"}
		registry := NewRegistry()
		node := NewNode[*providerLifecycle]("manual")
		if err := registry.Register(node, logger); err != nil {
			t.Fatalf("Register() failed: %v", err)
		}
		var injected providerCapability
		mustProvide(t, registry, "service", func(capability providerCapability) *providerService {
			injected = capability
			return &providerService{}
		})

		if _, err := registry.Compile(); err != nil {
			t.Fatalf("Compile() failed: %v", err)
		}
		if injected != logger {
			t.Fatal("provider did not receive specifically declared manual owner")
		}
	})

	t.Run("erased dynamic type is not injectable", func(t *testing.T) {
		t.Parallel()

		logger := &providerLifecycle{identity: "manual"}
		registry := NewRegistry()
		node := NewNode[Lifecycle]("manual")
		if err := registry.Register(node, Lifecycle(logger)); err != nil {
			t.Fatalf("Register() failed: %v", err)
		}
		var constructorCalls atomic.Int32
		mustProvide(t, registry, "service", func(providerCapability) *providerService {
			constructorCalls.Add(1)
			return &providerService{}
		})

		if err := registry.Bind[providerCapability](node); !errors.Is(err, ErrInvalidBinding) {
			t.Fatalf("Bind() error = %v, want ErrInvalidBinding", err)
		}
		if _, err := registry.Compile(); !errors.Is(err, ErrNotRegistered) {
			t.Fatalf("Compile() error = %v, want ErrNotRegistered", err)
		}
		if constructorCalls.Load() != 0 {
			t.Fatalf("constructor calls = %d, want 0", constructorCalls.Load())
		}
	})

	t.Run("declared interface value preserves its static type", func(t *testing.T) {
		t.Parallel()

		logger := &providerLifecycle{identity: "manual interface"}
		registry := NewRegistry()
		node := NewNode[lifecycleProviderCapability]("manual interface")
		if err := registry.Register(node, lifecycleProviderCapability(logger)); err != nil {
			t.Fatalf("Register() failed: %v", err)
		}
		var injected lifecycleProviderCapability
		mustProvide(t, registry, "service", func(capability lifecycleProviderCapability) *providerService {
			injected = capability
			return &providerService{}
		})

		if _, err := registry.Compile(); err != nil {
			t.Fatalf("Compile() failed: %v", err)
		}
		if injected != logger {
			t.Fatal("provider did not receive the manually registered interface value")
		}
	})
}

func TestProvideAllowsUnusedDuplicateDeclaredTypes(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	mustProvide(t, registry, "first", func() *providerLifecycle {
		return &providerLifecycle{identity: "first"}
	})
	mustProvide(t, registry, "second", func() *providerLifecycle {
		return &providerLifecycle{identity: "second"}
	})
	compiled, err := registry.Compile()
	if err != nil {
		t.Fatalf("Compile() failed: %v", err)
	}
	if got := len(compiled.core.entries); got != 2 {
		t.Fatalf("runtime entries = %d, want 2", got)
	}
}

func TestBindDisambiguatesDuplicateExactTypes(t *testing.T) {
	t.Parallel()

	first := &providerLifecycle{identity: "first"}
	second := &providerLifecycle{identity: "second"}
	registry := NewRegistry()
	mustProvide(t, registry, "first", func() *providerLifecycle { return first })
	secondRef := mustProvide(t, registry, "second", func() *providerLifecycle { return second })
	var injected *providerLifecycle
	mustProvide(t, registry, "service", func(owner *providerLifecycle) *providerService {
		injected = owner
		return &providerService{}
	})

	if _, err := registry.Compile(); !errors.Is(err, ErrAmbiguousDependency) {
		t.Fatalf("Compile() error = %v, want ErrAmbiguousDependency", err)
	}
	if err := registry.Bind[*providerLifecycle](secondRef); err != nil {
		t.Fatalf("Bind() failed: %v", err)
	}
	if _, err := registry.Compile(); err != nil {
		t.Fatalf("Compile() after Bind() failed: %v", err)
	}
	if injected != second {
		t.Fatal("binding did not select the second exact-type owner")
	}
}

func TestProvideValidatesConstructorWithoutRegisteringIt(t *testing.T) {
	t.Parallel()

	var typedNil func() *providerLifecycle
	tests := []struct {
		name        string
		constructor any
	}{
		{name: "nil", constructor: nil},
		{name: "non-function", constructor: 42},
		{name: "typed nil", constructor: typedNil},
		{name: "variadic", constructor: func(...providerCapability) *providerLifecycle { return nil }},
		{name: "no results", constructor: func() {}},
		{name: "three results", constructor: func() (*providerLifecycle, int, error) { return nil, 0, nil }},
		{name: "non-error second result", constructor: func() (*providerLifecycle, concreteProviderError) { return nil, concreteProviderError{} }},
		{name: "non-lifecycle result", constructor: func() string { return "value" }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			registry := NewRegistry()
			if ref, err := registry.Provide("invalid", test.constructor); !errors.Is(err, ErrInvalidConstructor) || ref != nil {
				t.Fatalf("Provide() = (%v, %v), want nil and ErrInvalidConstructor", ref, err)
			}
			compiled, err := registry.Compile()
			if err != nil {
				t.Fatalf("Compile() after rejected Provide() failed: %v", err)
			}
			if len(compiled.core.entries) != 0 {
				t.Fatalf("rejected constructor registered %d entries", len(compiled.core.entries))
			}
		})
	}
}

func TestProvideAndBindValidateRegistryAndReferences(t *testing.T) {
	t.Parallel()

	var nilRegistry *Registry
	if _, err := nilRegistry.Provide("owner", func() *providerLifecycle { return &providerLifecycle{} }); err == nil {
		t.Fatal("Provide() on nil registry returned nil error")
	}
	if err := nilRegistry.Bind[providerCapability](nil); err == nil {
		t.Fatal("Bind() on nil registry returned nil error")
	}

	registry := NewRegistry()
	var nilNode *Node[*providerLifecycle]
	if _, err := registry.Provide("invalid-order", func() *providerLifecycle { return &providerLifecycle{} }, nilNode); !errors.Is(err, ErrInvalidNode) {
		t.Fatalf("Provide() invalid order dependency error = %v, want ErrInvalidNode", err)
	}
	node := NewNode[*providerLifecycle]("order")
	if _, err := registry.Provide("duplicate-order", func() *providerLifecycle { return &providerLifecycle{} }, node, node); !errors.Is(err, ErrDuplicateDependency) {
		t.Fatalf("Provide() duplicate order dependency error = %v, want ErrDuplicateDependency", err)
	}
	if err := registry.Bind[providerCapability](nil); !errors.Is(err, ErrInvalidNode) {
		t.Fatalf("Bind(nil) error = %v, want ErrInvalidNode", err)
	}

	foreign := NewRegistry()
	foreignRef := mustProvide(t, foreign, "foreign", func() *providerLifecycle {
		return &providerLifecycle{}
	})
	if err := registry.Bind[providerCapability](foreignRef); !errors.Is(err, ErrNotRegistered) {
		t.Fatalf("Bind(foreign) error = %v, want ErrNotRegistered", err)
	}

	ownerRef := mustProvide(t, registry, "owner", func() *providerLifecycle {
		return &providerLifecycle{}
	})
	if err := registry.Bind[fmt.Stringer](ownerRef); !errors.Is(err, ErrInvalidBinding) {
		t.Fatalf("Bind(incompatible) error = %v, want ErrInvalidBinding", err)
	}
	if err := registry.Bind[providerCapability](ownerRef); err != nil {
		t.Fatalf("Bind() failed: %v", err)
	}
	if err := registry.Bind[providerCapability](ownerRef); !errors.Is(err, ErrInvalidBinding) {
		t.Fatalf("duplicate Bind() error = %v, want ErrInvalidBinding", err)
	}
	if err := registry.Bind[alternateProviderCapability](ownerRef); err != nil {
		t.Fatalf("second capability Bind() failed: %v", err)
	}

	if _, err := registry.Compile(); err != nil {
		t.Fatalf("Compile() failed: %v", err)
	}
	if _, err := registry.Provide("late", func() *providerLifecycle { return &providerLifecycle{} }); !errors.Is(err, ErrRegistryConsumed) {
		t.Fatalf("Provide() after Compile() error = %v, want ErrRegistryConsumed", err)
	}
	if err := registry.Bind[providerCapability](nil); !errors.Is(err, ErrRegistryConsumed) {
		t.Fatalf("Bind() after Compile() error = %v, want ErrRegistryConsumed", err)
	}
}

func TestCompileValidatesCompleteGraphBeforeConstruction(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	var loggerCalls atomic.Int32
	var serviceCalls atomic.Int32
	mustProvide(t, registry, "logger", func() *providerLifecycle {
		loggerCalls.Add(1)
		return &providerLifecycle{}
	})
	mustProvide(t, registry, "service", func(
		*providerLifecycle,
		*providerConfig,
	) *providerService {
		serviceCalls.Add(1)
		return &providerService{}
	})

	if _, err := registry.Compile(); !errors.Is(err, ErrNotRegistered) {
		t.Fatalf("Compile() error = %v, want ErrNotRegistered", err)
	}
	if loggerCalls.Load() != 0 || serviceCalls.Load() != 0 {
		t.Fatalf("constructors ran before full validation: logger=%d service=%d", loggerCalls.Load(), serviceCalls.Load())
	}

	configNode := NewNode[*providerConfig]("config")
	if err := registry.Register(configNode, &providerConfig{}); err != nil {
		t.Fatalf("Register(config) after failed Compile() failed: %v", err)
	}
	if _, err := registry.Compile(); err != nil {
		t.Fatalf("Compile() after repair failed: %v", err)
	}
	if loggerCalls.Load() != 1 || serviceCalls.Load() != 1 {
		t.Fatalf("constructor calls after repair: logger=%d service=%d, want 1 each", loggerCalls.Load(), serviceCalls.Load())
	}
}

func TestProviderAutomaticAndExplicitOrderingRules(t *testing.T) {
	t.Parallel()

	t.Run("order-only dependency", func(t *testing.T) {
		t.Parallel()

		registry := NewRegistry()
		dependency := NewNode[*providerConfig]("config")
		if err := registry.Register(dependency, &providerConfig{}); err != nil {
			t.Fatalf("Register() failed: %v", err)
		}
		mustProvide(t, registry, "service", func() *providerService {
			return &providerService{}
		}, dependency)
		compiled, err := registry.Compile()
		if err != nil {
			t.Fatalf("Compile() failed: %v", err)
		}
		if got, want := frontierLabels(compiled), [][]string{{"config"}, {"service"}}; !equalFrontiers(got, want) {
			t.Fatalf("frontiers = %v, want %v", got, want)
		}
	})

	t.Run("automatic plus explicit duplicate", func(t *testing.T) {
		t.Parallel()

		var loggerCalls atomic.Int32
		var serviceCalls atomic.Int32
		registry := NewRegistry()
		loggerRef := mustProvide(t, registry, "logger", func() *providerLifecycle {
			loggerCalls.Add(1)
			return &providerLifecycle{}
		})
		mustProvide(t, registry, "service", func(providerCapability) *providerService {
			serviceCalls.Add(1)
			return &providerService{}
		}, loggerRef)
		if _, err := registry.Compile(); !errors.Is(err, ErrDuplicateDependency) {
			t.Fatalf("Compile() error = %v, want ErrDuplicateDependency", err)
		}
		if loggerCalls.Load() != 0 || serviceCalls.Load() != 0 {
			t.Fatalf("constructors ran after duplicate edge: logger=%d service=%d", loggerCalls.Load(), serviceCalls.Load())
		}
	})

	t.Run("repeated parameters share one edge", func(t *testing.T) {
		t.Parallel()

		logger := &providerLifecycle{}
		registry := NewRegistry()
		mustProvide(t, registry, "logger", func() *providerLifecycle { return logger })
		var first providerCapability
		var second providerCapability
		mustProvide(t, registry, "service", func(a, b providerCapability) *providerService {
			first, second = a, b
			return &providerService{}
		})
		compiled, err := registry.Compile()
		if err != nil {
			t.Fatalf("Compile() failed: %v", err)
		}
		if first != logger || second != logger {
			t.Fatal("repeated parameters did not receive the same owner")
		}
		serviceEntry := runtimeEntryByLabel(t, compiled, "service")
		if len(serviceEntry.dependencies) != 1 {
			t.Fatalf("service dependency edges = %d, want 1", len(serviceEntry.dependencies))
		}
	})

	t.Run("unified manual-provider cycle", func(t *testing.T) {
		t.Parallel()

		var constructorCalls atomic.Int32
		registry := NewRegistry()
		manual := NewNode[*providerConfig]("manual")
		providerRef := mustProvide(t, registry, "provider", func() *providerLifecycle {
			constructorCalls.Add(1)
			return &providerLifecycle{}
		}, manual)
		if err := registry.Register(manual, &providerConfig{}, providerRef); err != nil {
			t.Fatalf("Register() failed: %v", err)
		}
		if _, err := registry.Compile(); !errors.Is(err, ErrCyclicDependency) {
			t.Fatalf("Compile() error = %v, want ErrCyclicDependency", err)
		}
		if constructorCalls.Load() != 0 {
			t.Fatalf("constructor calls = %d, want 0", constructorCalls.Load())
		}
	})
}

func TestProviderWrapperBindingKeepsBorrowedOwnership(t *testing.T) {
	t.Parallel()

	var events []string
	raw := &providerLifecycle{
		identity: "raw store",
		LifecycleFuncs: LifecycleFuncs{OnStop: func(context.Context) error {
			events = append(events, "raw")
			return nil
		}},
	}
	wrapped := &alternateProviderLifecycle{
		identity: "permitted store",
		LifecycleFuncs: LifecycleFuncs{OnStop: func(context.Context) error {
			events = append(events, "wrapper")
			return nil
		}},
	}
	httpOwner := &providerService{LifecycleFuncs: LifecycleFuncs{
		OnStop: func(context.Context) error {
			events = append(events, "http")
			return nil
		},
	}}

	registry := NewRegistry()
	mustProvide(t, registry, "raw store", func() *providerLifecycle { return raw })
	var borrowedStore *providerLifecycle
	wrapperRef := mustProvide(t, registry, "permitted store", func(
		borrowed *providerLifecycle,
	) *alternateProviderLifecycle {
		borrowedStore = borrowed
		return wrapped
	})
	if err := registry.Bind[providerCapability](wrapperRef); err != nil {
		t.Fatalf("Bind() failed: %v", err)
	}
	var injected providerCapability
	mustProvide(t, registry, "http", func(store providerCapability) *providerService {
		injected = store
		return httpOwner
	})

	compiled, err := registry.Compile()
	if err != nil {
		t.Fatalf("Compile() failed: %v", err)
	}
	if injected != wrapped {
		t.Fatal("HTTP owner did not receive the bound wrapper")
	}
	if borrowedStore != raw {
		t.Fatal("wrapper did not receive the raw store")
	}
	if err := compiled.Start(t.Context()); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	if err := compiled.Stop(t.Context()); err != nil {
		t.Fatalf("Stop() failed: %v", err)
	}
	if got, want := strings.Join(events, ","), "http,wrapper,raw"; got != want {
		t.Fatalf("stop events = %q, want %q", got, want)
	}
}

func TestConditionalProviderRegistrationConstructsOnlySelectedOwner(t *testing.T) {
	t.Parallel()

	for _, cancelStart := range []bool{false, true} {
		name := "successful start"
		if cancelStart {
			name = "cancelled start"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var kernelCalls atomic.Int32
			var userspaceCalls atomic.Int32
			type daemonSystem struct {
				selected string
			}
			system := &daemonSystem{}
			stop := make(chan struct{})
			joined := make(chan struct{})
			entered := make(chan struct{})
			release := make(chan struct{})
			if !cancelStart {
				close(release)
			}
			var stopOnce sync.Once
			stopDaemon := func() {
				stopOnce.Do(func() { close(stop) })
			}

			registry := NewRegistry()
			useUserspace := true
			if useUserspace {
				mustProvide(t, registry, "userspace tunnel", func() *userspaceTunnelOwner {
					userspaceCalls.Add(1)
					return &userspaceTunnelOwner{}
				})
			} else {
				mustProvide(t, registry, "kernel tunnel", func() *kernelTunnelOwner {
					kernelCalls.Add(1)
					return &kernelTunnelOwner{}
				})
			}
			mustProvide(t, registry, "daemon", func(tunnel tunnelCapability) *providerService {
				return &providerService{LifecycleFuncs: LifecycleFuncs{
					OnStart: func(ctx context.Context) error {
						close(entered)
						ready := make(chan struct{})
						go func() {
							defer close(joined)
							select {
							case <-release:
								system.selected = tunnel.Mode()
								close(ready)
								<-stop
							case <-stop:
							}
						}()
						select {
						case <-ready:
							return nil
						case <-ctx.Done():
							stopDaemon()
							<-joined
							return ctx.Err()
						}
					},
					OnStop: func(context.Context) error {
						stopDaemon()
						<-joined
						return nil
					},
				}}
			})

			compiled, err := registry.Compile()
			if err != nil {
				t.Fatalf("Compile() failed: %v", err)
			}
			if kernelCalls.Load() != 0 || userspaceCalls.Load() != 1 {
				t.Fatalf("constructor calls = kernel:%d userspace:%d, want 0 and 1", kernelCalls.Load(), userspaceCalls.Load())
			}

			if cancelStart {
				startContext, cancel := context.WithCancel(t.Context())
				startResult := make(chan error, 1)
				go func() { startResult <- compiled.Start(startContext) }()
				<-entered
				cancel()
				if err := <-startResult; !errors.Is(err, context.Canceled) {
					t.Fatalf("Start() error = %v, want context.Canceled", err)
				}
			} else {
				if err := compiled.Start(t.Context()); err != nil {
					t.Fatalf("Start() failed: %v", err)
				}
				if system.selected != "userspace" {
					t.Fatalf("selected tunnel = %q, want userspace", system.selected)
				}
			}
			if err := compiled.Stop(t.Context()); err != nil {
				t.Fatalf("Stop() failed: %v", err)
			}
			if err := compiled.Stop(t.Context()); err != nil {
				t.Fatalf("second Stop() failed: %v", err)
			}
			select {
			case <-joined:
			default:
				t.Fatal("daemon goroutine was not joined before Stop returned")
			}
		})
	}
}

func TestProviderConstructionFailuresConsumeRegistryWithoutLifecycleCalls(t *testing.T) {
	constructionCause := errors.New("construction cause")
	tests := []struct {
		name           string
		constructor    func(*atomic.Int32) any
		wantCause      error
		wantInvalid    bool
		wantPanicStack bool
	}{
		{
			name: "returned error",
			constructor: func(calls *atomic.Int32) any {
				return func() (*providerLifecycle, error) {
					calls.Add(1)
					return &providerLifecycle{}, constructionCause
				}
			},
			wantCause: constructionCause,
		},
		{
			name: "typed nil",
			constructor: func(calls *atomic.Int32) any {
				return func() *providerLifecycle {
					calls.Add(1)
					return nil
				}
			},
			wantInvalid: true,
		},
		{
			name: "typed nil behind interface",
			constructor: func(calls *atomic.Int32) any {
				return func() lifecycleProviderCapability {
					calls.Add(1)
					var lifecycle *providerLifecycle
					return lifecycle
				}
			},
			wantInvalid: true,
		},
		{
			name: "error panic",
			constructor: func(calls *atomic.Int32) any {
				return func() *providerLifecycle {
					calls.Add(1)
					panic(constructionCause)
				}
			},
			wantCause:      constructionCause,
			wantPanicStack: true,
		},
		{
			name: "value panic",
			constructor: func(calls *atomic.Int32) any {
				return func() *providerLifecycle {
					calls.Add(1)
					panic("boom")
				}
			},
			wantPanicStack: true,
		},
		{
			name: "nil panic",
			constructor: func(calls *atomic.Int32) any {
				return func() *providerLifecycle {
					calls.Add(1)
					panic(nil)
				}
			},
			wantPanicStack: true,
		},
		{
			name: "goexit",
			constructor: func(calls *atomic.Int32) any {
				return func() *providerLifecycle {
					calls.Add(1)
					runtime.Goexit()
					return nil
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var successfulCalls atomic.Int32
			var failureCalls atomic.Int32
			var laterCalls atomic.Int32
			var lifecycleCalls atomic.Int32
			registry := NewRegistry()
			manual := NewNode[LifecycleFuncs]("manual")
			if err := registry.Register(manual, LifecycleFuncs{
				OnConfigure: func(context.Context) error { lifecycleCalls.Add(1); return nil },
				OnStart:     func(context.Context) error { lifecycleCalls.Add(1); return nil },
				OnStop:      func(context.Context) error { lifecycleCalls.Add(1); return nil },
			}); err != nil {
				t.Fatalf("Register() failed: %v", err)
			}
			successRef := mustProvide(t, registry, "a-success", func() *alternateProviderLifecycle {
				successfulCalls.Add(1)
				return &alternateProviderLifecycle{}
			})
			mustProvide(t, registry, "z-failure", test.constructor(&failureCalls))
			mustProvide(t, registry, "zz-later", func() *providerConfig {
				laterCalls.Add(1)
				return &providerConfig{}
			})

			compiled, err := registry.Compile()
			if compiled != nil || !errors.Is(err, ErrConstruction) {
				t.Fatalf("Compile() = (%v, %v), want nil and ErrConstruction", compiled, err)
			}
			if test.wantCause != nil && !errors.Is(err, test.wantCause) {
				t.Fatalf("Compile() error = %v, want cause %v", err, test.wantCause)
			}
			if test.wantInvalid && !errors.Is(err, ErrInvalidLifecycle) {
				t.Fatalf("Compile() error = %v, want ErrInvalidLifecycle", err)
			}
			if test.wantPanicStack && !strings.Contains(err.Error(), "goroutine") {
				t.Fatalf("panic error does not contain a stack: %v", err)
			}
			if successfulCalls.Load() != 1 || failureCalls.Load() != 1 || laterCalls.Load() != 0 {
				t.Fatalf(
					"constructor calls = successful:%d failure:%d later:%d, want 1, 1, 0",
					successfulCalls.Load(),
					failureCalls.Load(),
					laterCalls.Load(),
				)
			}
			if lifecycleCalls.Load() != 0 {
				t.Fatalf("lifecycle callback calls = %d, want 0", lifecycleCalls.Load())
			}
			if _, err := registry.Compile(); !errors.Is(err, ErrRegistryConsumed) {
				t.Fatalf("second Compile() error = %v, want ErrRegistryConsumed", err)
			}
			if _, err := registry.Provide("late", func() *providerLifecycle { return &providerLifecycle{} }); !errors.Is(err, ErrRegistryConsumed) {
				t.Fatalf("Provide() after failure error = %v, want ErrRegistryConsumed", err)
			}
			if err := registry.Bind[alternateProviderCapability](successRef); !errors.Is(err, ErrRegistryConsumed) {
				t.Fatalf("Bind() after failure error = %v, want ErrRegistryConsumed", err)
			}
			if err := registry.Register(NewNode[LifecycleFuncs]("late"), LifecycleFuncs{}); !errors.Is(err, ErrRegistryConsumed) {
				t.Fatalf("Register() after failure error = %v, want ErrRegistryConsumed", err)
			}
		})
	}
}

func TestProviderConstructorReentryObservesConsumedRegistry(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	var providerRef NodeRef
	var provideErr error
	var bindErr error
	var registerErr error
	var compileErr error
	providerRef = mustProvide(t, registry, "owner", func() *providerLifecycle {
		_, provideErr = registry.Provide("late", func() *providerLifecycle {
			return &providerLifecycle{}
		})
		bindErr = registry.Bind[providerCapability](providerRef)
		registerErr = registry.Register(NewNode[LifecycleFuncs]("late"), LifecycleFuncs{})
		_, compileErr = registry.Compile()
		return &providerLifecycle{}
	})

	if _, err := registry.Compile(); err != nil {
		t.Fatalf("outer Compile() failed: %v", err)
	}
	for operation, err := range map[string]error{
		"Provide":  provideErr,
		"Bind":     bindErr,
		"Register": registerErr,
		"Compile":  compileErr,
	} {
		if !errors.Is(err, ErrRegistryConsumed) {
			t.Errorf("reentrant %s error = %v, want ErrRegistryConsumed", operation, err)
		}
	}
}

func TestProviderErrorMethodsCannotStrandCompile(t *testing.T) {
	t.Parallel()

	cause := goexitConstructionError{}
	tests := []struct {
		name        string
		constructor any
	}{
		{
			name: "returned error",
			constructor: func() (*providerLifecycle, error) {
				return &providerLifecycle{}, cause
			},
		},
		{
			name: "panic error",
			constructor: func() *providerLifecycle {
				panic(cause)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			registry := NewRegistry()
			mustProvide(t, registry, "malicious error", test.constructor)
			compileResult := make(chan error, 1)
			go func() {
				_, err := registry.Compile()
				compileResult <- err
			}()

			select {
			case err := <-compileResult:
				if !errors.Is(err, ErrConstruction) {
					t.Fatalf("Compile() error = %v, want ErrConstruction", err)
				}
				if !errors.Is(err, cause) {
					t.Fatal("Compile() error did not preserve constructor cause")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("Compile() hung while handling a constructor-owned error")
			}
			if _, err := registry.Compile(); !errors.Is(err, ErrRegistryConsumed) {
				t.Fatalf("second Compile() error = %v, want ErrRegistryConsumed", err)
			}
		})
	}
}

func TestIndependentProvidersConstructInDeterministicOrder(t *testing.T) {
	t.Parallel()

	var events []string
	registry := NewRegistry()
	mustProvide(t, registry, "zulu", func() *providerLifecycle {
		events = append(events, "zulu")
		return &providerLifecycle{}
	})
	mustProvide(t, registry, "same", func() *alternateProviderLifecycle {
		events = append(events, "same-first")
		return &alternateProviderLifecycle{}
	})
	mustProvide(t, registry, "alpha", func() *providerConfig {
		events = append(events, "alpha")
		return &providerConfig{}
	})
	mustProvide(t, registry, "same", func() LifecycleFuncs {
		events = append(events, "same-second")
		return LifecycleFuncs{}
	})

	if _, err := registry.Compile(); err != nil {
		t.Fatalf("Compile() failed: %v", err)
	}
	if got, want := strings.Join(events, ","), "alpha,same-first,same-second,zulu"; got != want {
		t.Fatalf("construction order = %q, want %q", got, want)
	}
}

func mustProvide(
	t *testing.T,
	registry *Registry,
	label string,
	constructor any,
	orderAfter ...NodeRef,
) NodeRef {
	t.Helper()
	ref, err := registry.Provide(label, constructor, orderAfter...)
	if err != nil {
		t.Fatalf("Provide(%q) failed: %v", label, err)
	}
	return ref
}

func runtimeEntryByLabel(t *testing.T, runtime *Runtime, label string) runtimeEntry {
	t.Helper()
	for _, entry := range runtime.core.entries {
		if entry.node.label == label {
			return entry
		}
	}
	t.Fatalf("runtime has no entry labeled %q", label)
	return runtimeEntry{}
}

func equalFrontiers(left, right [][]string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if strings.Join(left[index], "\x00") != strings.Join(right[index], "\x00") {
			return false
		}
	}
	return true
}
