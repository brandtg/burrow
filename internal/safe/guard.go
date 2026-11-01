package safe

// State is the tri-state outcome of a guard. Busy and Unknown both stop the
// action (fail closed).
type State int

const (
	// Safe means the action may proceed.
	Safe State = iota
	// Busy means the owner of the resource is active.
	Busy
	// Unknown means safety could not be proven.
	Unknown
)

func (s State) String() string {
	switch s {
	case Safe:
		return "safe"
	case Busy:
		return "busy"
	default:
		return "unknown"
	}
}

// GuardResult carries a guard decision and its reason.
type GuardResult struct {
	State  State
	Reason string
}

// Pass reports whether the action may proceed.
func (g GuardResult) Pass() bool { return g.State == Safe }

// Err returns a guard-stop error when the result is not Safe, else nil.
func (g GuardResult) Err() error {
	if g.Pass() {
		return nil
	}
	return GuardStop("%s", g.Reason)
}

// OK builds a passing guard result.
func OK() GuardResult { return GuardResult{State: Safe} }

// Stop builds a failing guard result. An empty reason is replaced with a
// generic fail-closed message so an empty reason can never read as safe.
func Stop(state State, reason string) GuardResult {
	if state == Safe {
		state = Unknown
	}
	if reason == "" {
		reason = "cannot prove the action is safe right now"
	}
	return GuardResult{State: state, Reason: reason}
}

// BusyGuard builds a Busy guard result.
func BusyGuard(reason string) GuardResult { return Stop(Busy, reason) }

// UnknownGuard builds an Unknown guard result.
func UnknownGuard(reason string) GuardResult { return Stop(Unknown, reason) }
