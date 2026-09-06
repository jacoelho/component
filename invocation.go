package component

import (
	"context"
	"runtime/debug"
)

type invocationResult struct {
	index int
	value any
	err   error
}

// One invocation owns the factory-to-Start handoff. Its deferred publication
// preserves transferred ownership through a Start panic or Goexit.
func invokeNode(ctx context.Context, index int, node *definition, arguments []any, value any, results chan<- invocationResult) {
	result := invocationResult{index: index, value: value}
	phase := "construct"
	returned := false
	defer func() {
		if recovered := recover(); recovered != nil {
			cause, _ := recovered.(error)
			failure := nodeFailure(index, node, phase, cause)
			failure.kind = ErrPanic
			failure.panicValue = recovered
			failure.Stack = debug.Stack()
			result.err = failure
		} else if !returned {
			failure := nodeFailure(index, node, phase, nil)
			failure.kind = ErrAborted
			result.err = failure
		}
		results <- result
	}()
	if value != nil {
		phase = "stop"
		result.err = node.stop(ctx, value)
	} else {
		result.value, result.err = node.create(ctx, arguments)
		if result.err == nil && node.start != nil {
			phase = "start"
			result.err = node.start(ctx, result.value)
		}
	}
	if result.err != nil {
		result.err = nodeFailure(index, node, phase, result.err)
	}
	returned = true
}
