package runtime

// State represents runtime lifecycle state.
type State int

const (
	StateIdle State = iota
	StateStarting
	StateStarted
	StateStopping
	StateStopped
	StateStartFailed
	StateStopFailed
)

func (s State) String() string {
	switch s {
	case StateIdle:
		return "idle"
	case StateStarting:
		return "starting"
	case StateStarted:
		return "started"
	case StateStopping:
		return "stopping"
	case StateStopped:
		return "stopped"
	case StateStartFailed:
		return "start_failed"
	case StateStopFailed:
		return "stop_failed"
	default:
		return "unknown"
	}
}
