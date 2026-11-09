package component_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/jacoelho/component"
)

// freeAddr returns a loopback address that was free a moment ago. Serve
// listens on Addr itself, so the test cannot ask the kernel for port 0.
func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := new(net.ListenConfig).Listen(testContext(t), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve address: %v", err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("release address: %v", err)
	}
	return addr
}

func get(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return string(body), err
}

func newServer(addr string) func(string) *http.Server {
	return func(greeting string) *http.Server {
		return &http.Server{Addr: addr, Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, greeting)
		})}
	}
}

func TestServeListensAtStartAndShutsDownAtStop(t *testing.T) {
	addr := freeAddr(t)
	srv := component.MapValue(component.Value("hello"), newServer(addr), component.Serve())
	app := mustNew(t, []component.Root{srv})
	if err := app.Start(testContext(t)); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Start returns once the listener is bound, so the request needs no retry.
	body, err := get(testContext(t), "http://"+addr)
	if err != nil || body != "hello" {
		t.Fatalf("GET = %q, %v; want %q", body, err, "hello")
	}

	if err := app.Stop(testContext(t)); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, err := get(testContext(t), "http://"+addr); err == nil {
		t.Fatal("server still answers after Stop")
	}
}

func TestServeFailsStartWhenTheAddressIsTaken(t *testing.T) {
	taken, err := new(net.ListenConfig).Listen(testContext(t), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = taken.Close() })
	srv := component.MapValue(component.Value("x"), newServer(taken.Addr().String()),
		component.Serve(), component.Label[*http.Server]("http"))
	app := mustNew(t, []component.Root{srv})

	err = app.Start(testContext(t))
	var opErr *net.OpError
	if !errors.As(err, &opErr) {
		t.Fatalf("Start = %v, want a listen error", err)
	}
	requireNodeError(t, err, "http", component.PhaseStart, opErr)
	if err := app.Stop(testContext(t)); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestServeReleasesTheListenerWhenALaterStartHookFails(t *testing.T) {
	addr := freeAddr(t)
	refused := errors.New("refused")
	srv := component.MapValue(component.Value("x"), newServer(addr), component.Serve(),
		component.OnStart(func(*http.Server, context.Context) error { return refused }))
	app := mustNew(t, []component.Root{srv})
	requireIs(t, app.Start(testContext(t)), refused)

	if err := app.Stop(testContext(t)); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	l, err := new(net.ListenConfig).Listen(testContext(t), "tcp", addr)
	if err != nil {
		t.Fatalf("address still held after Stop: %v", err)
	}
	_ = l.Close()
}

func TestServeClosesConnectionsWhenTheStopContextHasExpired(t *testing.T) {
	addr := freeAddr(t)
	inHandler, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() { close(release) })
	srv := component.ProvideValue(func() *http.Server {
		return &http.Server{Addr: addr, Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			close(inHandler)
			<-release
		})}
	}, component.Serve())
	app := mustNew(t, []component.Root{srv})
	if err := app.Start(testContext(t)); err != nil {
		t.Fatalf("Start: %v", err)
	}
	requestErr := make(chan error, 1)
	go func() { _, err := get(testContext(t), "http://"+addr); requestErr <- err }()
	<-inHandler

	stopCtx, cancel := context.WithCancel(testContext(t))
	cancel()
	err := app.Stop(stopCtx)

	requireIs(t, err, context.Canceled)
	// Close drops the in-flight connection at once. Without it the request
	// would only end when its own 10s test context expires.
	select {
	case err := <-requestErr:
		if err == nil || errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("in-flight request ended with %v; want the connection closed by Close", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight request still open 2s after Stop; Close did not drop it")
	}
}

func TestServeKeepsListenersSeparatePerApp(t *testing.T) {
	first, second := freeAddr(t), freeAddr(t)
	addrs := []string{first, second}
	n := 0
	srv := component.ProvideValue(func() *http.Server {
		a := addrs[n]
		n++
		return newServer(a)("app" + a)
	}, component.Serve())

	a := mustNew(t, []component.Root{srv})
	b := mustNew(t, []component.Root{srv})
	for _, app := range []*component.App{a, b} {
		if err := app.Start(testContext(t)); err != nil {
			t.Fatalf("Start: %v", err)
		}
	}
	if err := a.Stop(testContext(t)); err != nil {
		t.Fatalf("Stop a: %v", err)
	}
	// Stopping the first App must not close the second App's listener.
	body, err := get(testContext(t), "http://"+second)
	if err != nil || body != "app"+second {
		t.Fatalf("GET second = %q, %v; want it still serving", body, err)
	}
	if err := b.Stop(testContext(t)); err != nil {
		t.Fatalf("Stop b: %v", err)
	}
}
