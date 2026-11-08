package component

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
)

// Option attaches a hook or a label to the node a constructor declares. Only
// the constructor that creates a value can attach hooks to it, so the owner
// of a resource decides how it starts and stops. The zero Option is invalid.
type Option[T any] struct {
	hooks func() []hook
	err   error
	label string
}

type hookKind uint8

const (
	hookStart hookKind = iota + 1
	hookStop
	hookClose
	hookTask
	hookDrain
)

type hook struct {
	run  func(context.Context, any) error
	kind hookKind
}

func hookOption[T any](name string, kind hookKind, f func(T, context.Context) error) Option[T] {
	if f == nil {
		return Option[T]{err: fmt.Errorf("%w: %s requires a non-nil function", ErrInvalidDefinition, name)}
	}
	h := []hook{{kind: kind, run: func(ctx context.Context, v any) error { return f(as[T](v), ctx) }}}
	return Option[T]{hooks: func() []hook { return h }}
}

// OnStart runs f during start, after every dependency has started. A node's
// OnStart hooks run in option order. An error stops Start.
func OnStart[T any](f func(T, context.Context) error) Option[T] {
	return hookOption("OnStart", hookStart, f)
}

// OnStop runs f during Stop if the node's start began, after its dependents
// have stopped and before its own tasks are awaited, so a call such as
// (*http.Server).Shutdown can end a task blocked in Serve.
func OnStop[T any](f func(T, context.Context) error) Option[T] {
	return hookOption("OnStop", hookStop, f)
}

// OnClose runs f during Stop for every built node, started or not, at the same
// point as OnStop. It fits methods such as (*sql.DB).Close.
func OnClose[T any](f func(T) error) Option[T] {
	if f == nil {
		return Option[T]{err: fmt.Errorf("%w: OnClose requires a non-nil function", ErrInvalidDefinition)}
	}
	return hookOption("OnClose", hookClose, func(v T, _ context.Context) error { return f(v) })
}

// Go runs f in its own goroutine once the node's OnStart hooks have
// succeeded. Its context is cancelled when Stop reaches the node. A return
// before Stop begins ends Wait; a return of the context's error after
// cancellation is not a failure.
func Go[T any](f func(T, context.Context) error) Option[T] {
	return hookOption("Go", hookTask, f)
}

// OnDrain runs f at the beginning of Stop, before any node stops, if the
// node's start began. Use it to fail readiness checks and let traffic move
// away while every dependency is still running.
func OnDrain[T any](f func(T, context.Context) error) Option[T] {
	return hookOption("OnDrain", hookDrain, f)
}

// Label names the node in errors and Plan. The default is the constructor's
// function name. Labels are not keys: duplicates are allowed.
func Label[T any](name string) Option[T] {
	if name == "" {
		return Option[T]{err: fmt.Errorf("%w: Label requires a non-empty name", ErrInvalidDefinition)}
	}
	return Option[T]{label: name}
}

// Serve runs an *http.Server. Start listens on Addr (":http" when empty), so
// an address error fails Start. A task then calls Serve, and
// http.ErrServerClosed counts as a clean exit. Stop calls Shutdown with the
// stop context, then Close if that context has expired.
func Serve() Option[*http.Server] {
	return Option[*http.Server]{hooks: func() []hook {
		// One listener per App: the hooks are produced once per App.
		var ln net.Listener
		listen := func(ctx context.Context, v any) error {
			srv := as[*http.Server](v)
			addr := srv.Addr
			if addr == "" {
				addr = ":http"
			}
			l, err := new(net.ListenConfig).Listen(ctx, "tcp", addr)
			if err != nil {
				return err
			}
			ln = l
			return nil
		}
		serve := func(_ context.Context, v any) error {
			err := as[*http.Server](v).Serve(ln)
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		}
		shutdown := func(ctx context.Context, v any) error {
			srv := as[*http.Server](v)
			err := srv.Shutdown(ctx)
			if ctx.Err() != nil {
				err = errors.Join(err, srv.Close())
			}
			// Serve closes the listener it was given; this covers a listener
			// whose Serve task never launched.
			if ln != nil {
				if cerr := ln.Close(); cerr != nil && !errors.Is(cerr, net.ErrClosed) {
					err = errors.Join(err, cerr)
				}
			}
			return err
		}
		return []hook{
			{kind: hookStart, run: listen},
			{kind: hookTask, run: serve},
			{kind: hookStop, run: shutdown},
		}
	}}
}
