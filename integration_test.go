package component_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jacoelho/component"
)

// The pipeline types model the ownership boundary that a push system needs to
// make explicit: each stage owns its worker and its input queue, and a stage
// depends on the downstream capability it sends to.
type pipelineTrace struct {
	mu     sync.Mutex
	events []string
}

func (trace *pipelineTrace) add(event string) {
	trace.mu.Lock()
	trace.events = append(trace.events, event)
	trace.mu.Unlock()
}

func (trace *pipelineTrace) snapshot() []string {
	trace.mu.Lock()
	defer trace.mu.Unlock()
	return append([]string(nil), trace.events...)
}

type pipelineSink struct {
	input chan int
	ready chan struct{}
	done  chan struct{}

	stopOnce sync.Once
	trace    *pipelineTrace

	mu     sync.Mutex
	values []int
}

func newPipelineSink(trace *pipelineTrace) *pipelineSink {
	return &pipelineSink{
		input: make(chan int),
		ready: make(chan struct{}),
		done:  make(chan struct{}),
		trace: trace,
	}
}

func (sink *pipelineSink) Start(ctx context.Context) error {
	go func() {
		close(sink.ready)
		for value := range sink.input {
			sink.mu.Lock()
			sink.values = append(sink.values, value)
			sink.mu.Unlock()
		}
		close(sink.done)
	}()
	return waitPipelineReady(ctx, sink.ready)
}

func (sink *pipelineSink) Stop(ctx context.Context) error {
	sink.stopOnce.Do(func() { close(sink.input) })
	select {
	case <-sink.done:
		sink.trace.add("sink.stop")
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type pipelineProcessor struct {
	input chan int
	sink  *pipelineSink
	ready chan struct{}
	done  chan struct{}

	stopOnce sync.Once
	trace    *pipelineTrace
}

func newPipelineProcessor(sink *pipelineSink, trace *pipelineTrace) *pipelineProcessor {
	return &pipelineProcessor{
		input: make(chan int),
		sink:  sink,
		ready: make(chan struct{}),
		done:  make(chan struct{}),
		trace: trace,
	}
}

func (processor *pipelineProcessor) Start(ctx context.Context) error {
	if err := requirePipelineReady(ctx, processor.sink.ready, "sink"); err != nil {
		return err
	}
	go func() {
		close(processor.ready)
		for value := range processor.input {
			// The sink remains ready until this worker has drained. There is no
			// cancellation branch here: accepted input must reach the sink.
			processor.sink.input <- value * 2
		}
		close(processor.done)
	}()
	return waitPipelineReady(ctx, processor.ready)
}

func (processor *pipelineProcessor) Stop(ctx context.Context) error {
	processor.stopOnce.Do(func() { close(processor.input) })
	select {
	case <-processor.done:
		processor.trace.add("processor.stop")
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type pipelineSource struct {
	processor *pipelineProcessor
	messages  []int
	release   chan struct{}
	ready     chan struct{}
	done      chan struct{}

	trace       *pipelineTrace
	releaseOnce sync.Once
	stopOnce    sync.Once
}

func newPipelineSource(processor *pipelineProcessor, trace *pipelineTrace) *pipelineSource {
	return &pipelineSource{
		processor: processor,
		messages:  []int{3, 7, 11},
		release:   make(chan struct{}),
		ready:     make(chan struct{}),
		done:      make(chan struct{}),
		trace:     trace,
	}
}

func (source *pipelineSource) releaseMessages() {
	source.releaseOnce.Do(func() { close(source.release) })
}

func (source *pipelineSource) Start(ctx context.Context) error {
	if err := requirePipelineReady(ctx, source.processor.ready, "processor"); err != nil {
		return err
	}
	go func() {
		close(source.ready)
		<-source.release
		for _, message := range source.messages {
			source.processor.input <- message
		}
		close(source.done)
	}()
	return waitPipelineReady(ctx, source.ready)
}

func (source *pipelineSource) Stop(ctx context.Context) error {
	select {
	case <-source.done:
		source.stopOnce.Do(func() { source.trace.add("source.stop") })
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func waitPipelineReady(ctx context.Context, ready <-chan struct{}) error {
	select {
	case <-ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func requirePipelineReady(
	ctx context.Context,
	ready <-chan struct{},
	name string,
) error {
	select {
	case <-ready:
		return nil
	default:
		if err := ctx.Err(); err != nil {
			return err
		}
		return fmt.Errorf("%s was not ready before dependent start", name)
	}
}

func (sink *pipelineSink) valuesSnapshot() []int {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return append([]int(nil), sink.values...)
}

func TestPipelineReadinessDrainAndReverseStop(t *testing.T) {
	trace := &pipelineTrace{}
	var source *pipelineSource

	sinkRef := component.ProvideValue(
		func() *pipelineSink { return newPipelineSink(trace) },
		component.Managed[*pipelineSink](),
	)
	processorRef := component.MapValue(sinkRef,
		func(sink *pipelineSink) *pipelineProcessor {
			return newPipelineProcessor(sink, trace)
		},
		component.Managed[*pipelineProcessor](),
	)
	sourceRef := component.MapValue(processorRef,
		func(processor *pipelineProcessor) *pipelineSource {
			source = newPipelineSource(processor, trace)
			return source
		},
		component.Managed[*pipelineSource](),
	)

	runtime, err := component.New(sourceRef)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	t.Cleanup(func() {
		if source != nil {
			source.releaseMessages()
		}
		cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), time.Second)
		defer cancelCleanup()
		_ = runtime.Stop(cleanupCtx)
	})

	startCtx, cancelStart := context.WithTimeout(t.Context(), time.Second)
	defer cancelStart()
	if err := runtime.Start(startCtx); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	sink, err := runtime.Value(sinkRef)
	if err != nil {
		t.Fatalf("Value(sink) failed: %v", err)
	}
	sourceValue, err := runtime.Value(sourceRef)
	if err != nil {
		t.Fatalf("Value(source) failed: %v", err)
	}
	select {
	case <-sink.ready:
	default:
		t.Fatal("Start returned before sink readiness handshake")
	}
	select {
	case <-sourceValue.processor.ready:
	default:
		t.Fatal("Start returned before processor readiness handshake")
	}

	sourceValue.releaseMessages()
	stopCtx, cancelStop := context.WithTimeout(t.Context(), time.Second)
	defer cancelStop()
	if err := runtime.Stop(stopCtx); err != nil {
		t.Fatalf("Stop() failed: %v", err)
	}

	if got, want := sink.valuesSnapshot(), []int{6, 14, 22}; !reflect.DeepEqual(got, want) {
		t.Fatalf("sink values = %v, want %v", got, want)
	}
	if got, want := trace.snapshot(), []string{"source.stop", "processor.stop", "sink.stop"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("stop order = %v, want %v", got, want)
	}
}

type integrationHTTPHandler struct {
	ready   atomic.Bool
	entered chan struct{}
	release chan struct{}

	enterOnce   sync.Once
	releaseOnce sync.Once
}

func (handler *integrationHTTPHandler) releaseRequest() {
	handler.releaseOnce.Do(func() { close(handler.release) })
}

func (handler *integrationHTTPHandler) ServeHTTP(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if !handler.ready.Load() {
		http.Error(writer, "service unavailable", http.StatusServiceUnavailable)
		return
	}
	handler.enterOnce.Do(func() { close(handler.entered) })
	select {
	case <-handler.release:
	case <-request.Context().Done():
		return
	}
	fmt.Fprintln(writer, "ok")
}

type integrationHTTPServer struct {
	server   *http.Server
	listener net.Listener
	handler  *integrationHTTPHandler

	accepting       chan struct{}
	done            chan struct{}
	shutdownStarted chan struct{}
	listenerClosed  chan struct{}

	failStart atomic.Bool
	started   atomic.Bool

	closeListenerOnce sync.Once
	shutdownOnce      sync.Once
	mu                sync.Mutex
	serveErr          error
	closeErr          error
}

type readinessListener struct {
	net.Listener
	accepting chan struct{}
	once      sync.Once
}

func (listener *readinessListener) Accept() (net.Conn, error) {
	listener.once.Do(func() { close(listener.accepting) })
	return listener.Listener.Accept()
}

func newIntegrationHTTPServer(
	ctx context.Context,
	handler *integrationHTTPHandler,
) (*integrationHTTPServer, error) {
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen for HTTP server: %w", err)
	}
	return &integrationHTTPServer{
		server:          &http.Server{Handler: handler},
		listener:        listener,
		handler:         handler,
		accepting:       make(chan struct{}),
		done:            make(chan struct{}),
		shutdownStarted: make(chan struct{}),
		listenerClosed:  make(chan struct{}),
	}, nil
}

func (server *integrationHTTPServer) Start(ctx context.Context) error {
	if server.failStart.Load() {
		return errors.New("HTTP server startup rejected")
	}
	server.started.Store(true)
	listener := &readinessListener{
		Listener:  server.listener,
		accepting: server.accepting,
	}
	go func() {
		err := server.server.Serve(listener)
		server.mu.Lock()
		server.serveErr = err
		server.mu.Unlock()
		close(server.done)
	}()
	select {
	case <-server.accepting:
		server.handler.ready.Store(true)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (server *integrationHTTPServer) closeListener() error {
	server.closeListenerOnce.Do(func() {
		err := server.listener.Close()
		if errors.Is(err, net.ErrClosed) {
			err = nil
		}
		server.mu.Lock()
		server.closeErr = err
		server.mu.Unlock()
		close(server.listenerClosed)
	})
	server.mu.Lock()
	defer server.mu.Unlock()
	return server.closeErr
}

func (server *integrationHTTPServer) Stop(ctx context.Context) error {
	server.shutdownOnce.Do(func() {
		server.handler.ready.Store(false)
		close(server.shutdownStarted)
	})
	if !server.started.Load() {
		return server.closeListener()
	}
	if err := server.server.Shutdown(ctx); err != nil {
		return err
	}
	select {
	case <-server.done:
		server.mu.Lock()
		err := server.serveErr
		server.mu.Unlock()
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (server *integrationHTTPServer) address() string {
	return server.listener.Addr().String()
}

func TestHTTPServerOwnsListenerAndGracefullyDrains(t *testing.T) {
	handler := &integrationHTTPHandler{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	handlerRef := component.Value(handler)
	serverRef := component.MapContext(handlerRef,
		func(ctx context.Context, handler *integrationHTTPHandler) (*integrationHTTPServer, error) {
			return newIntegrationHTTPServer(ctx, handler)
		},
		component.Managed[*integrationHTTPServer](),
	)
	runtime, err := component.New(serverRef)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	var requestResult chan error
	var stopResult chan error
	t.Cleanup(func() {
		handler.releaseRequest()
		if requestResult != nil {
			select {
			case <-requestResult:
			case <-time.After(time.Second):
			}
		}
		if stopResult != nil {
			select {
			case <-stopResult:
			case <-time.After(time.Second):
			}
		}
		cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), time.Second)
		defer cancelCleanup()
		_ = runtime.Stop(cleanupCtx)
	})

	startCtx, cancelStart := context.WithTimeout(t.Context(), time.Second)
	defer cancelStart()
	if err := runtime.Start(startCtx); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	server, err := runtime.Value(serverRef)
	if err != nil {
		t.Fatalf("Value(server) failed: %v", err)
	}

	requestCtx, cancelRequest := context.WithTimeout(t.Context(), time.Second)
	defer cancelRequest()
	request, err := http.NewRequestWithContext(
		requestCtx,
		http.MethodGet,
		"http://"+server.address(),
		http.NoBody,
	)
	if err != nil {
		t.Fatalf("NewRequestWithContext() failed: %v", err)
	}
	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	requestResult = make(chan error, 1)
	go func() {
		defer close(requestResult)
		response, err := client.Do(request)
		if err != nil {
			requestResult <- err
			return
		}
		body, readErr := io.ReadAll(response.Body)
		closeErr := response.Body.Close()
		switch {
		case readErr != nil:
			requestResult <- readErr
		case closeErr != nil:
			requestResult <- closeErr
		case response.StatusCode != http.StatusOK:
			requestResult <- fmt.Errorf("HTTP status = %d, want %d", response.StatusCode, http.StatusOK)
		case string(body) != "ok\n":
			requestResult <- fmt.Errorf("HTTP body = %q, want %q", body, "ok\n")
		default:
			requestResult <- nil
		}
	}()
	select {
	case <-handler.entered:
	case <-requestCtx.Done():
		t.Fatal("HTTP handler did not become active")
	}

	stopCtx, cancelStop := context.WithTimeout(t.Context(), time.Second)
	stopResult = make(chan error, 1)
	go func() {
		defer close(stopResult)
		stopResult <- runtime.Stop(stopCtx)
	}()
	select {
	case <-server.shutdownStarted:
	case <-stopCtx.Done():
		t.Fatal("HTTP shutdown did not start")
	}
	cancelStop()
	var firstStopErr error
	select {
	case firstStopErr = <-stopResult:
	case <-time.After(5 * time.Second):
		t.Fatal("canceled Stop did not join its callback")
	}
	if !errors.Is(firstStopErr, component.ErrCleanupPending) || !errors.Is(firstStopErr, context.Canceled) {
		t.Fatalf("first Stop() error = %v, want cleanup pending and context canceled", firstStopErr)
	}

	handler.releaseRequest()
	select {
	case err := <-requestResult:
		if err != nil {
			t.Fatalf("HTTP request failed: %v", err)
		}
	case <-requestCtx.Done():
		t.Fatal("HTTP request did not complete")
	}
	secondStopCtx, cancelSecondStop := context.WithTimeout(t.Context(), time.Second)
	defer cancelSecondStop()
	if err := runtime.Stop(secondStopCtx); err != nil {
		t.Fatalf("retry Stop() failed: %v", err)
	}
	if server.handler.ready.Load() {
		t.Fatal("HTTP handler remains ready after Stop")
	}
	server.mu.Lock()
	serveErr := server.serveErr
	server.mu.Unlock()
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		t.Fatalf("Serve() failed: %v", serveErr)
	}
}

func TestHTTPListenerIsReleasedAfterStartFailure(t *testing.T) {
	handler := &integrationHTTPHandler{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	handlerRef := component.Value(handler)
	var created *integrationHTTPServer
	serverRef := component.MapContext(handlerRef,
		func(ctx context.Context, handler *integrationHTTPHandler) (*integrationHTTPServer, error) {
			server, err := newIntegrationHTTPServer(ctx, handler)
			if err == nil {
				server.failStart.Store(true)
				created = server
			}
			return server, err
		},
		component.Managed[*integrationHTTPServer](),
	)
	runtime, err := component.New(serverRef)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	t.Cleanup(func() {
		handler.releaseRequest()
		cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), time.Second)
		defer cancelCleanup()
		_ = runtime.Stop(cleanupCtx)
	})

	startCtx, cancelStart := context.WithTimeout(t.Context(), time.Second)
	defer cancelStart()
	if err := runtime.Start(startCtx); err == nil {
		t.Fatal("Start() succeeded, want startup failure")
	}
	if created == nil {
		t.Fatal("context-taking factory did not acquire the listener")
	}

	stopCtx, cancelStop := context.WithTimeout(t.Context(), time.Second)
	defer cancelStop()
	if err := runtime.Stop(stopCtx); err != nil {
		t.Fatalf("Stop() after failed Start failed: %v", err)
	}
	select {
	case <-created.listenerClosed:
	case <-stopCtx.Done():
		t.Fatal("failed-start listener was not released")
	}
	if created.handler.ready.Load() {
		t.Fatal("failed-start handler became ready")
	}
}
