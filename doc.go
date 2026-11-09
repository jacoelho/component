// Package component wires an application as a typed build graph and runs it:
// it builds every value once, starts them in dependency order and stops them
// in reverse.
//
// Each node is one call. Value declares an existing value. ProvideValue,
// Provide and ProvideContext declare a constructor without inputs; MapValue,
// Map and MapContext, with forms for up to eight inputs, declare one with
// inputs. The suffix follows the constructor's shape: no error, an error, or a
// context and an error. Every call returns a Ref, and the refs a constructor
// takes are its dependencies, so the graph is acyclic by construction and
// type-checked by the compiler.
//
// Options attach the lifecycle of the value a constructor creates: OnStart,
// OnStop, OnClose, Go (a supervised task), OnDrain, Label, and Serve for an
// *http.Server. Method expressions fit them directly:
//
//	db := component.MapContext(cfg, openDB,
//		component.OnStart((*sql.DB).PingContext),
//		component.OnClose((*sql.DB).Close))
//
// New builds the graph from its roots, Start starts it, Wait blocks until a
// signal or a task exit, and Stop drains, stops and closes it within the
// stop context's deadline. See the README for a guide and ARCHITECTURE.md for
// the full contract.
package component
