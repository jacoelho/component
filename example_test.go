package component_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/jacoelho/component"
)

type exampleLogger struct{}

func (*exampleLogger) Configure(context.Context) error {
	fmt.Println("configure logger")
	return nil
}

func (*exampleLogger) Start(context.Context) error {
	fmt.Println("start logger")
	return nil
}

func (*exampleLogger) Stop(context.Context) error {
	fmt.Println("stop logger")
	return nil
}

type exampleService struct {
	logger *exampleLogger
}

func newExampleService(logger *exampleLogger) (*exampleService, error) {
	if logger == nil {
		return nil, fmt.Errorf("logger is required")
	}
	return &exampleService{logger: logger}, nil
}

func (*exampleService) Configure(context.Context) error {
	fmt.Println("configure service")
	return nil
}

func (*exampleService) Start(context.Context) error {
	fmt.Println("start service")
	return nil
}

func (*exampleService) Stop(context.Context) error {
	fmt.Println("stop service")
	return nil
}

func ExampleRegistry_Register() {
	logger := &exampleLogger{}
	service, err := newExampleService(logger)
	if err != nil {
		panic(err)
	}

	loggerNode := component.NewNode[*exampleLogger]("logger")
	serviceNode := component.NewNode[*exampleService]("service")
	registry := component.NewRegistry()
	if err := registry.Register(loggerNode, logger); err != nil {
		panic(err)
	}
	if err := registry.Register(serviceNode, service, loggerNode); err != nil {
		panic(err)
	}

	runtime, err := registry.Compile()
	if err != nil {
		panic(err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		panic(err)
	}
	if err := runtime.Stop(context.Background()); err != nil {
		panic(err)
	}

	// Output:
	// configure logger
	// configure service
	// start logger
	// start service
	// stop service
	// stop logger
}

func ExampleRegistry_Provide() {
	registry := component.NewRegistry()
	if _, err := registry.Provide("logger", func() *exampleLogger {
		return &exampleLogger{}
	}); err != nil {
		panic(err)
	}
	if _, err := registry.Provide("service", newExampleService); err != nil {
		panic(err)
	}

	runtime, err := registry.Compile()
	if err != nil {
		panic(err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		panic(err)
	}
	if err := runtime.Stop(context.Background()); err != nil {
		panic(err)
	}

	// Output:
	// configure logger
	// configure service
	// start logger
	// start service
	// stop service
	// stop logger
}

type runService func(context.Context) error

func newRunService(logger *slog.Logger) runService {
	return func(context.Context) error {
		logger.Info("run")
		return nil
	}
}

func newHandler(run runService) func(context.Context) error {
	return func(ctx context.Context) error { return run(ctx) }
}

func Example_closureComposition() {
	logger := slog.New(slog.DiscardHandler)
	service := newRunService(logger)
	handler := newHandler(service)

	// The logger, service closure, and handler are ordinary Go values. None
	// owns lifecycle state, so none needs a graph node.
	if err := handler(context.Background()); err != nil {
		panic(err)
	}
	fmt.Println("handled")

	// Output: handled
}

func ExampleLifecycleFuncs() {
	logger := slog.New(slog.DiscardHandler)
	startService := func(context.Context) error {
		logger.Info("started")
		return nil
	}
	stopService := func(context.Context) error {
		logger.Info("stopped")
		return nil
	}

	lifecycle := component.LifecycleFuncs{
		OnStart: startService,
		OnStop:  stopService,
	}
	if err := lifecycle.Configure(context.Background()); err != nil {
		panic(err)
	}
	fmt.Println("nil Configure is a no-op")

	// Output: nil Configure is a no-op
}

type exampleHTTPHandler struct {
	ready   atomic.Bool
	entered chan<- struct{}
	release <-chan struct{}
}

func (*exampleHTTPHandler) Configure(context.Context) error {
	return nil
}

func (handler *exampleHTTPHandler) Start(context.Context) error {
	handler.ready.Store(true)
	return nil
}

func (handler *exampleHTTPHandler) Stop(context.Context) error {
	handler.ready.Store(false)
	return nil
}

func (handler *exampleHTTPHandler) ServeHTTP(
	w http.ResponseWriter,
	_ *http.Request,
) {
	if !handler.ready.Load() {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}
	if handler.entered != nil {
		handler.entered <- struct{}{}
	}
	if handler.release != nil {
		<-handler.release
	}
	fmt.Fprintln(w, "ok")
}

type exampleHTTPServer struct {
	server   *http.Server
	listener net.Listener
	done     chan struct{}
	errMu    sync.Mutex
	serveErr error
}

func newExampleHTTPServer(
	address string,
	handler http.Handler,
) *exampleHTTPServer {
	return &exampleHTTPServer{server: &http.Server{
		Addr:    address,
		Handler: handler,
	}}
}

func (server *exampleHTTPServer) Configure(ctx context.Context) error {
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(ctx, "tcp", server.server.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", server.server.Addr, err)
	}
	server.listener = listener
	return nil
}

func (server *exampleHTTPServer) Start(context.Context) error {
	if server.listener == nil {
		return errors.New("http server is not configured")
	}

	server.done = make(chan struct{})
	go func() {
		err := server.server.Serve(server.listener)
		server.errMu.Lock()
		server.serveErr = err
		server.errMu.Unlock()
		close(server.done)
	}()
	return nil
}

func (server *exampleHTTPServer) Stop(ctx context.Context) error {
	if server.listener == nil {
		return nil
	}
	if server.done == nil {
		err := server.listener.Close()
		if errors.Is(err, net.ErrClosed) {
			return nil
		}
		return err
	}
	if err := server.server.Shutdown(ctx); err != nil {
		return err
	}
	<-server.done
	return nil
}

func (server *exampleHTTPServer) Done() <-chan struct{} {
	return server.done
}

func (server *exampleHTTPServer) Err() error {
	server.errMu.Lock()
	defer server.errMu.Unlock()
	if errors.Is(server.serveErr, http.ErrServerClosed) {
		return nil
	}
	return server.serveErr
}

func (server *exampleHTTPServer) Address() string {
	return server.listener.Addr().String()
}

func ExampleRuntime_gracefulHTTPShutdown() {
	signalCtx, stopSignals := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stopSignals()

	handler := &exampleHTTPHandler{}
	server := newExampleHTTPServer(":8080", handler)
	handlerNode := component.NewNode[*exampleHTTPHandler]("http-handler")
	serverNode := component.NewNode[*exampleHTTPServer]("http-server")
	registry := component.NewRegistry()
	if err := registry.Register(handlerNode, handler); err != nil {
		panic(err)
	}
	if err := registry.Register(serverNode, server, handlerNode); err != nil {
		panic(err)
	}
	runtime, err := registry.Compile()
	if err != nil {
		panic(err)
	}

	startCtx, cancelStart := context.WithTimeout(signalCtx, 10*time.Second)
	err = runtime.Start(startCtx)
	cancelStart()
	if err != nil {
		stopCtx, cancelStop := context.WithTimeout(
			context.Background(),
			10*time.Second,
		)
		stopErr := runtime.Stop(stopCtx)
		cancelStop()
		panic(errors.Join(err, stopErr))
	}

	var serveErr error
	select {
	case <-signalCtx.Done():
		stopSignals()
	case <-server.Done():
		serveErr = server.Err()
	}

	stopCtx, cancelStop := context.WithTimeout(
		context.Background(),
		10*time.Second,
	)
	stopErr := runtime.Stop(stopCtx)
	cancelStop()
	if serveErr == nil {
		serveErr = server.Err()
	}
	if err := errors.Join(serveErr, stopErr); err != nil {
		panic(err)
	}
}

func TestExampleHTTPServerLifecycle(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	handler := &exampleHTTPHandler{entered: entered, release: release}
	server := newExampleHTTPServer("127.0.0.1:0", handler)
	shutdownStarted := make(chan struct{})
	server.server.RegisterOnShutdown(func() { close(shutdownStarted) })
	handlerNode := component.NewNode[*exampleHTTPHandler]("http-handler")
	serverNode := component.NewNode[*exampleHTTPServer]("http-server")
	registry := component.NewRegistry()
	if err := registry.Register(handlerNode, handler); err != nil {
		t.Fatalf("Register(handler) failed: %v", err)
	}
	if err := registry.Register(serverNode, server, handlerNode); err != nil {
		t.Fatalf("Register(server) failed: %v", err)
	}
	runtime, err := registry.Compile()
	if err != nil {
		t.Fatalf("Compile() failed: %v", err)
	}
	if err := runtime.Start(t.Context()); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	request, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"http://"+server.Address(),
		http.NoBody,
	)
	if err != nil {
		t.Fatalf("NewRequestWithContext() failed: %v", err)
	}
	requestResult := make(chan error, 1)
	go func() {
		response, err := client.Do(request)
		if err != nil {
			requestResult <- err
			return
		}
		closeErr := response.Body.Close()
		if response.StatusCode != http.StatusOK {
			requestResult <- fmt.Errorf(
				"HTTP status = %d, want %d",
				response.StatusCode,
				http.StatusOK,
			)
			return
		}
		requestResult <- closeErr
	}()
	select {
	case <-entered:
	case <-t.Context().Done():
		t.Fatal("HTTP handler did not start")
	}

	stopCtx, cancelStop := context.WithTimeout(t.Context(), time.Second)
	defer cancelStop()
	stopResult := make(chan error, 1)
	go func() { stopResult <- runtime.Stop(stopCtx) }()
	select {
	case <-shutdownStarted:
	case <-stopCtx.Done():
		t.Fatal("HTTP shutdown did not start")
	}
	if !handler.ready.Load() {
		t.Fatal("handler stopped while serving a request")
	}
	select {
	case err := <-stopResult:
		t.Fatalf("Stop() returned before the request completed: %v", err)
	default:
	}
	close(release)
	select {
	case err := <-requestResult:
		if err != nil {
			t.Fatalf("HTTP request failed: %v", err)
		}
	case <-stopCtx.Done():
		t.Fatal("HTTP request did not complete")
	}
	select {
	case err := <-stopResult:
		if err != nil {
			t.Fatalf("Stop() failed: %v", err)
		}
	case <-stopCtx.Done():
		t.Fatal("Stop() did not complete")
	}
	if handler.ready.Load() {
		t.Fatal("handler remains ready after Stop()")
	}
	if err := server.Err(); err != nil {
		t.Fatalf("Serve() failed: %v", err)
	}
}
