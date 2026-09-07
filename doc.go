// Package component builds immutable, typed construction graphs and runs their
// owned resources in dependency order.
//
// References are composition-only values. Value supplies a borrowed value;
// ProvideValue, Provide, and ProvideContext create top-level nodes, while
// MapValue, Map, and MapContext, including their 2 through 4 forms, derive
// nodes from typed inputs. An Ownership value from Managed declares ownership
// at the factory that creates a resource. Owned values implement Lifecycle;
// its Start and Stop methods receive the caller's context. Constructors
// receive ordinary application values, never a resolver.
//
// Runtime.Start and Runtime.Stop receive independent caller-created contexts.
// Runtime runs one node at a time in deterministic dependency order. Start does
// not roll back acquired resources. Applications call Stop after a failed
// Start, and Stop methods must be safe to retry after partial failure. A failed
// stop retains that node's dependencies until a later Stop succeeds.
// See README.md for the quickstart and ARCHITECTURE.md for the ownership,
// lifecycle, error, and execution contracts.
package component
