// Package component builds immutable, typed construction graphs and runs their
// owned resources in dependency order.
//
// References are composition-only values. Value supplies a borrowed value;
// Provide, TryProvide, and ProvideContext create top-level nodes, while Map,
// TryMap, and MapContext derive nodes from typed inputs. An Ownership value
// from Managed declares ownership at the factory that creates a resource.
// Owned values implement Lifecycle; its Start and Stop methods receive the
// caller's context. Constructors receive ordinary application values, never a
// resolver.
//
// Runtime.Start and Runtime.Stop receive independent caller-created contexts.
// Start does not roll back acquired resources. Applications call Stop after a
// failed Start, and Stop methods must be safe to retry after partial failure.
// See README.md for the quickstart and ARCHITECTURE.md for the ownership,
// lifecycle, error, and execution contracts.
package component
