// Package component constructs, configures, starts, and stops lifecycle owners
// in dependency order.
//
// Applications may construct owners directly and declare order with Register,
// or register inert constructors with Registry.Provide. Provide parameters are
// resolved from declared owner types when Compile validates the complete graph.
// A dependency starts before its dependent and stops after it. Injected owners
// are borrowed: each owner stops only resources it creates.
package component
