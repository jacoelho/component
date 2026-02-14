package statemachine

import "fmt"

type transitionKey[S comparable, E comparable] struct {
	state S
	event E
}

// Rule defines a single state transition edge.
// If Err is set, the transition fails and To/Action are ignored.
type Rule[S comparable, E comparable, A any] struct {
	From   S
	Event  E
	To     S
	Action A
	Err    error
}

// Engine evaluates state transitions for a finite-state machine.
type Engine[S comparable, E comparable, A any] struct {
	rules          map[transitionKey[S, E]]Rule[S, E, A]
	defaultInvalid func(S, E) error
}

func NewEngine[S comparable, E comparable, A any](
	rules []Rule[S, E, A],
	defaultInvalid func(S, E) error,
) *Engine[S, E, A] {
	compiled := make(map[transitionKey[S, E]]Rule[S, E, A], len(rules))
	for _, rule := range rules {
		key := transitionKey[S, E]{state: rule.From, event: rule.Event}
		compiled[key] = rule
	}

	return &Engine[S, E, A]{
		rules:          compiled,
		defaultInvalid: defaultInvalid,
	}
}

// Transition returns the next state and action for a (state,event) pair.
func (e *Engine[S, E, A]) Transition(from S, event E) (S, A, error) {
	var zeroAction A

	key := transitionKey[S, E]{state: from, event: event}
	rule, ok := e.rules[key]
	if !ok {
		if e.defaultInvalid != nil {
			return from, zeroAction, e.defaultInvalid(from, event)
		}
		return from, zeroAction, fmt.Errorf("no transition for state/event pair")
	}

	if rule.Err != nil {
		return from, zeroAction, rule.Err
	}

	return rule.To, rule.Action, nil
}
