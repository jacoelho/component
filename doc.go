// Package component configures, starts, and stops lifecycle owners in
// dependency order.
//
// Construction and value wiring remain ordinary Go. Graph edges
// express lifecycle ordering only: a dependency starts before its dependent
// and stops after it.
package component
