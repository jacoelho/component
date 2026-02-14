package statemachine

import (
	"errors"
	"testing"
)

func TestEngineTransition(t *testing.T) {
	type state int
	type event int
	type action int

	const (
		stateIdle state = iota
		stateActive
	)

	const (
		eventStart event = iota
		eventStop
	)

	const (
		actionNone action = iota
		actionRun
	)

	sentinelInvalid := errors.New("invalid transition")
	sentinelBlocked := errors.New("blocked transition")

	engine := NewEngine([]Rule[state, event, action]{
		{From: stateIdle, Event: eventStart, To: stateActive, Action: actionRun},
		{From: stateActive, Event: eventStop, Err: sentinelBlocked},
	}, func(state, event) error {
		return sentinelInvalid
	})

	next, act, err := engine.Transition(stateIdle, eventStart)
	if err != nil {
		t.Fatalf("expected nil error for valid transition, got %v", err)
	}
	if next != stateActive {
		t.Fatalf("unexpected next state: got %v, want %v", next, stateActive)
	}
	if act != actionRun {
		t.Fatalf("unexpected action: got %v, want %v", act, actionRun)
	}

	next, act, err = engine.Transition(stateActive, eventStop)
	if !errors.Is(err, sentinelBlocked) {
		t.Fatalf("expected blocked transition error, got %v", err)
	}
	if next != stateActive {
		t.Fatalf("error transition should keep current state, got %v", next)
	}
	if act != actionNone {
		t.Fatalf("error transition should have zero action, got %v", act)
	}

	next, act, err = engine.Transition(stateIdle, eventStop)
	if !errors.Is(err, sentinelInvalid) {
		t.Fatalf("expected invalid transition error, got %v", err)
	}
	if next != stateIdle {
		t.Fatalf("invalid transition should keep current state, got %v", next)
	}
	if act != actionNone {
		t.Fatalf("invalid transition should have zero action, got %v", act)
	}
}
