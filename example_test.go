package component_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/jacoelho/component"
)

// Store stands in for a connection pool such as *sql.DB.
type Store struct{ dsn string }

func OpenStore(dsn string) (*Store, error) {
	fmt.Println("open store")
	return &Store{dsn: dsn}, nil
}

func (s *Store) Ping(context.Context) error { fmt.Println("ping store"); return nil }
func (s *Store) Close() error               { fmt.Println("close store"); return nil }

// Worker consumes jobs until its context is cancelled.
type Worker struct{ store *Store }

func NewWorker(s *Store) *Worker { return &Worker{store: s} }

func (w *Worker) Run(ctx context.Context) error {
	<-ctx.Done()
	fmt.Println("worker stopped")
	return ctx.Err()
}

func Example() {
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
	// Output:
	// open store
	// ping store
	// worker stopped
	// close store
}

func ExampleStarted() {
	dsn := component.Value("postgres://localhost/app")
	store := component.Map(dsn, OpenStore,
		component.OnStart((*Store).Ping),
		component.OnClose((*Store).Close))
	// The migration runs in its constructor, so it needs a pinged store.
	migrated := component.MapValue(component.Started(store), func(*Store) bool {
		fmt.Println("migrate")
		return true
	})

	ctx := context.Background()
	app, err := component.New(ctx, []component.Root{migrated})
	if err != nil {
		log.Fatal(err)
	}
	if err := app.Stop(ctx); err != nil {
		log.Print(err)
	}
	// Output:
	// open store
	// ping store
	// migrate
	// close store
}

func ExampleReplace() {
	dsn := component.Value("postgres://localhost/app")
	store := component.Map(dsn, OpenStore, component.OnClose((*Store).Close))
	worker := component.MapValue(store, NewWorker)

	app, err := component.New(context.Background(), []component.Root{worker},
		component.Replace(store, &Store{dsn: "memory"}))
	if err != nil {
		log.Fatal(err)
	}
	w, _ := component.Get(app, worker)
	fmt.Println(w.store.dsn)
	// Output: memory
}

// runService is the program shape for a service: build, start, run until a
// signal or a task exit, then stop within a deadline. It is compiled, not
// run, because it binds a port and waits for a signal.
func runService() (err error) {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	handler := component.Value[http.Handler](http.NotFoundHandler())
	srv := component.MapValue(handler, func(h http.Handler) *http.Server {
		return &http.Server{Addr: ":8080", Handler: h, ReadHeaderTimeout: 5 * time.Second}
	}, component.Serve())

	app, err := component.New(ctx, []component.Root{srv})
	if err != nil {
		return err // New has already stopped what it built
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err = errors.Join(err, app.Stop(stopCtx))
	}()
	if err := app.Start(ctx); err != nil {
		return err
	}
	if err := app.Wait(ctx); ctx.Err() == nil {
		return err // a task exited before any signal arrived
	}
	return nil // a signal is a normal shutdown
}

var _ = runService

// StorageRefs is what a storage package would export: the refs other
// packages may depend on or replace.
type StorageRefs struct {
	Store  component.Ref[*Store]
	Worker component.Ref[*Worker]
}

// StorageGraph would live in the storage package. Its inputs are refs, so
// the caller decides where configuration comes from.
func StorageGraph(dsn component.Ref[string]) StorageRefs {
	store := component.Map(dsn, OpenStore,
		component.OnStart((*Store).Ping),
		component.OnClose((*Store).Close))
	worker := component.MapValue(store, NewWorker, component.Go((*Worker).Run))
	return StorageRefs{Store: store, Worker: worker}
}

func Example_packages() {
	storage := StorageGraph(component.Value("postgres://localhost/app"))
	// An API package would take storage.Store as an input; edges cross
	// packages without either package importing the other.
	report := component.MapValue(storage.Store, func(s *Store) string { return "store at " + s.dsn })

	ctx := context.Background()
	app, err := component.New(ctx, []component.Root{report, storage.Worker})
	if err != nil {
		log.Fatal(err)
	}
	r, _ := component.Get(app, report)
	fmt.Println(r)
	if err := app.Stop(ctx); err != nil {
		log.Print(err)
	}
	// Output:
	// open store
	// store at postgres://localhost/app
	// close store
}
