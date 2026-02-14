package runtime

import (
	"errors"
	"testing"
)

func TestLifecycleFSMTransitions(t *testing.T) {
	fsm := NewLifecycleFSM()

	cases := []struct {
		name       string
		from       State
		event      Event
		wantTo     State
		wantAction Action
		wantErr    error
	}{
		{
			name:       "start from idle",
			from:       StateIdle,
			event:      EventStartRequested,
			wantTo:     StateStarting,
			wantAction: ActionRunStart,
		},
		{
			name:       "start from stopped",
			from:       StateStopped,
			event:      EventStartRequested,
			wantTo:     StateStarting,
			wantAction: ActionRunStart,
		},
		{
			name:    "start from started",
			from:    StateStarted,
			event:   EventStartRequested,
			wantErr: ErrAlreadyStartedTransition,
		},
		{
			name:       "stop retry from failed stop",
			from:       StateFailedStop,
			event:      EventStopRequested,
			wantTo:     StateStopping,
			wantAction: ActionRunStop,
		},
		{
			name:       "start failure marks rolling back start",
			from:       StateStarting,
			event:      EventStartFailed,
			wantTo:     StateRollingBackStart,
			wantAction: ActionNone,
		},
		{
			name:       "rollback success marks stopped",
			from:       StateRollingBackStart,
			event:      EventStartRollbackSucceeded,
			wantTo:     StateStopped,
			wantAction: ActionNone,
		},
		{
			name:       "rollback failure marks failed start",
			from:       StateRollingBackStart,
			event:      EventStartRollbackFailed,
			wantTo:     StateFailedStart,
			wantAction: ActionNone,
		},
		{
			name:    "stop invalid while start rollback in progress",
			from:    StateRollingBackStart,
			event:   EventStopRequested,
			wantErr: ErrInvalidTransition,
		},
		{
			name:       "stop failure marks failed stop",
			from:       StateStopping,
			event:      EventStopFailed,
			wantTo:     StateFailedStop,
			wantAction: ActionNone,
		},
		{
			name:       "stop success marks stopped",
			from:       StateStopping,
			event:      EventStopSucceeded,
			wantTo:     StateStopped,
			wantAction: ActionNone,
		},
		{
			name:    "invalid stop from idle",
			from:    StateIdle,
			event:   EventStopRequested,
			wantErr: ErrInvalidTransition,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotTo, gotAction, err := fsm.Transition(tc.from, tc.event)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("expected error %v, got %v", tc.wantErr, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("expected nil error, got %v", err)
			}
			if gotTo != tc.wantTo {
				t.Fatalf("next state mismatch: got %v, want %v", gotTo, tc.wantTo)
			}
			if gotAction != tc.wantAction {
				t.Fatalf("action mismatch: got %v, want %v", gotAction, tc.wantAction)
			}
		})
	}
}
