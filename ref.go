package component

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strings"
)

// Ref is a typed identity of one node in a build graph. It holds no value:
// an App builds one value per reachable Ref. Two Refs are equal only when
// they come from the same call, so independent nodes of the same type stay
// distinct. The zero Ref is invalid.
type Ref[T any] struct {
	// A named marker keeps Ref[T] unconvertible to Ref[U], even when T and U
	// differ only in struct tags (a bare *T would allow that conversion). The
	// zero-length array keeps every Ref comparable, whatever T is.
	_    [0]*typeMarker[T]
	node *node
}

type typeMarker[T any] struct{}

// Root is a node an App is built from. Only Ref values implement it.
type Root interface {
	rootNode() *node
}

// In is a constructor input: a Ref[T], or Started applied to one. Only this
// package implements it.
type In[T any] interface {
	// The method mentions T so that a Ref[T] argument infers T.
	input() (Ref[T], bool)
}

func (r Ref[T]) rootNode() *node { return r.node }

func (r Ref[T]) input() (Ref[T], bool) { return r, false }

type startedInput[T any] struct{ ref Ref[T] }

func (s startedInput[T]) input() (Ref[T], bool) { return s.ref, true }

// Started marks an input that must be started, together with everything it
// depends on, before the consuming constructor runs. Use it when a
// constructor needs a running dependency, for example to read from a
// connection pool that OnStart has verified.
//
//nolint:iface // In is a closed interface; returning it lets Started(r) stand where a Ref is accepted.
func Started[T any](r Ref[T]) In[T] {
	return startedInput[T]{ref: r}
}

// Value declares an existing value. The App borrows it: no hooks run for it
// and nothing closes it. A function value is stored, never called.
func Value[T any](v T) Ref[T] {
	return Ref[T]{node: &node{
		label: "Value[" + reflect.TypeFor[T]().String() + "]",
		build: func(context.Context, []any) (any, error) { return v, nil },
	}}
}

// edge is one input of a node. started requires the input to have started
// before the node is built.
type edge struct {
	node    *node
	started bool
}

// node is the immutable declaration shared by every App built from it.
// Per-App state lives in instance.
type node struct {
	build func(context.Context, []any) (any, error)
	err   error
	label string
	// options produce the node's hooks once per App, so an option may keep
	// per-App state such as a listener.
	options []func() []hook
	inputs  []edge
}

func edgeOf[T any](in In[T]) (e edge) {
	// A nil In, or a nil pointer to a struct embedding a Ref, is reported by
	// New as an invalid reference rather than panicking here.
	defer func() {
		if recover() != nil {
			e = edge{}
		}
	}()
	if in == nil {
		return edge{}
	}
	ref, started := in.input()
	return edge{node: ref.node, started: started}
}

func rootOf(root Root) (n *node) {
	defer func() {
		if recover() != nil {
			n = nil
		}
	}()
	if root == nil {
		return nil
	}
	return root.rootNode()
}

// declare creates the node for a constructor. label is the default label.
func declare[R any](label string, inputs []edge, build func(context.Context, []any) (R, error), opts []Option[R]) Ref[R] {
	n := &node{
		label:  label,
		inputs: inputs,
		build: func(ctx context.Context, args []any) (any, error) {
			v, err := build(ctx, args)
			if err != nil {
				return nil, err
			}
			return v, nil
		},
	}
	// Every invalid option is reported, not only the last.
	var errs []error
	labelled := false
	for _, opt := range opts {
		switch {
		case opt.err != nil:
			errs = append(errs, opt.err)
		case opt.label != "":
			if labelled {
				errs = append(errs, fmt.Errorf("%w: more than one Label", ErrInvalidDefinition))
			}
			n.label, labelled = opt.label, true
		case opt.hooks != nil:
			n.options = append(n.options, opt.hooks)
		default:
			errs = append(errs, fmt.Errorf("%w: zero Option", ErrInvalidDefinition))
		}
	}
	n.err = errors.Join(errs...)
	return Ref[R]{node: n}
}

// invalid declares a node for a nil constructor, keeping its label so New
// can name it, and its inputs so New can check them.
func invalid[R any](name string, inputs []edge, opts []Option[R]) Ref[R] {
	r := declare[R](name, inputs, nil, opts)
	r.node.err = errors.Join(fmt.Errorf("%w: %s requires a non-nil constructor", ErrInvalidDefinition, name), r.node.err)
	return r
}

// as recovers a value stored as any. A nil interface value is stored as a nil
// any and comes back as the nil T. Any other mismatch is a bug in this
// package, so the assertion panics instead of handing out a zero value.
func as[T any](v any) T {
	if v == nil {
		var zero T
		return zero
	}
	t, ok := v.(T)
	if !ok {
		panic(fmt.Sprintf("component: internal error: stored %T where %s was declared", v, reflect.TypeFor[T]()))
	}
	return t
}

// funcLabel names a constructor by its function, without the import path:
// "storage.open" rather than "example.com/app/storage.open".
func funcLabel(fn any) string {
	f := runtime.FuncForPC(reflect.ValueOf(fn).Pointer())
	if f == nil {
		return reflect.TypeOf(fn).String()
	}
	name := f.Name()
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	return name
}
