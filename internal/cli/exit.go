package cli

import (
	"context"
	"errors"

	"github.com/brandtg/burrow/internal/safe"
)

// Exit codes.
const (
	ExitOK        = 0
	ExitGeneric   = 1
	ExitUsage     = 2
	ExitPartial   = 3
	ExitGuard     = 75
	ExitTimeout   = 124
	ExitInterrupt = 130
)

// ExitError carries an explicit process exit code.
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string {
	if e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func (e *ExitError) Unwrap() error { return e.Err }

// ExitCode maps an error to a process exit code.
func ExitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	var gs *safe.GuardStopError
	if errors.As(err, &gs) {
		return ExitGuard
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return ExitTimeout
	}
	return ExitGeneric
}
