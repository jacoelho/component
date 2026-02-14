package runtime

import (
	"errors"
	"fmt"

	"github.com/jacoelho/component/internal/statemachine"
)

type Event int

const (
	EventStartRequested Event = iota
	EventStartSucceeded
	EventStartFailed
	EventStartRollbackSucceeded
	EventStartRollbackFailed
	EventStopRequested
	EventStopSucceeded
	EventStopFailed
)

func (e Event) String() string {
	switch e {
	case EventStartRequested:
		return "startRequested"
	case EventStartSucceeded:
		return "startSucceeded"
	case EventStartFailed:
		return "startFailed"
	case EventStartRollbackSucceeded:
		return "startRollbackSucceeded"
	case EventStartRollbackFailed:
		return "startRollbackFailed"
	case EventStopRequested:
		return "stopRequested"
	case EventStopSucceeded:
		return "stopSucceeded"
	case EventStopFailed:
		return "stopFailed"
	default:
		return fmt.Sprintf("unknownEvent(%d)", e)
	}
}

type Action int

const (
	ActionNone Action = iota
	ActionRunStart
	ActionRunStop
)

func (a Action) String() string {
	switch a {
	case ActionNone:
		return "none"
	case ActionRunStart:
		return "runStart"
	case ActionRunStop:
		return "runStop"
	default:
		return fmt.Sprintf("unknownAction(%d)", a)
	}
}

var (
	ErrInvalidTransition        = errors.New("invalid transition")
	ErrAlreadyStartedTransition = errors.New("already started transition")
)

type LifecycleFSM struct {
	engine *statemachine.Engine[State, Event, Action]
}

func NewLifecycleFSM() *LifecycleFSM {
	rules := []statemachine.Rule[State, Event, Action]{
		{From: StateIdle, Event: EventStartRequested, To: StateStarting, Action: ActionRunStart},
		{From: StateStopped, Event: EventStartRequested, To: StateStarting, Action: ActionRunStart},
		{From: StateStarted, Event: EventStartRequested, Err: ErrAlreadyStartedTransition},

		{From: StateStarted, Event: EventStopRequested, To: StateStopping, Action: ActionRunStop},
		{From: StateFailedStart, Event: EventStopRequested, To: StateStopping, Action: ActionRunStop},
		{From: StateFailedStop, Event: EventStopRequested, To: StateStopping, Action: ActionRunStop},

		{From: StateStarting, Event: EventStartSucceeded, To: StateStarted, Action: ActionNone},
		{From: StateStarting, Event: EventStartFailed, To: StateRollingBackStart, Action: ActionNone},
		{From: StateRollingBackStart, Event: EventStartRollbackSucceeded, To: StateStopped, Action: ActionNone},
		{From: StateRollingBackStart, Event: EventStartRollbackFailed, To: StateFailedStart, Action: ActionNone},

		{From: StateStopping, Event: EventStopSucceeded, To: StateStopped, Action: ActionNone},
		{From: StateStopping, Event: EventStopFailed, To: StateFailedStop, Action: ActionNone},
	}

	engine := statemachine.NewEngine(rules, func(State, Event) error {
		return ErrInvalidTransition
	})

	return &LifecycleFSM{engine: engine}
}

func (fsm *LifecycleFSM) Transition(from State, event Event) (State, Action, error) {
	if fsm == nil || fsm.engine == nil {
		var zero Action
		return from, zero, ErrInvalidTransition
	}
	return fsm.engine.Transition(from, event)
}
