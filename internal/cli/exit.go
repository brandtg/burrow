package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/brandtg/burrow/internal/safe"
	"github.com/spf13/cobra"
)

const (
	// ExitOK is a completed run, including "nothing was selected".
	ExitOK = 0
	// ExitGeneric is an operational failure.
	ExitGeneric = 1
	// ExitUsage is an invocation problem: bad flags, bad config, or the
	// root-without-SUDO_USER refusal. Every command maps environment
	// resolution failures here so the same mistake is the same code.
	ExitUsage = 2
	// ExitPartial is a run where at least one item failed.
	ExitPartial = 3
	// ExitAborted is backing out of the interactive flow: the user quit the
	// preview or review screen, so nothing ran — distinct from a clean exit
	// where the plan simply had nothing selected.
	ExitAborted = 4
	// ExitGuard is a run where every attempt was stopped by a safety guard.
	ExitGuard = 75
	// ExitTimeout is a run where every attempt timed out.
	ExitTimeout = 124
	// ExitInterrupt is a run cut short by SIGINT/SIGTERM.
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

// noEffect rejects persistent flags that do nothing on the invoking command,
// so `burrow history --dry-run` is a usage error instead of a silently
// ignored flag. Call it at the top of a command's RunE with the names that
// command does not read.
func noEffect(cmd *cobra.Command, names ...string) error {
	for _, name := range names {
		if cmd.Flags().Changed(name) {
			return &ExitError{Code: ExitUsage, Err: fmt.Errorf("flag --%s has no effect on 'burrow %s'", name, cmd.Name())}
		}
	}
	return nil
}

// cleanFlowOnlyFlags are the persistent flags that only the clean flow reads.
const cleanFlowOnlyFlags = "dry-run yes plain color no-color"

func (a *App) rejectCleanFlowFlags(cmd *cobra.Command, extra ...string) error {
	return noEffect(cmd, append(strings.Fields(cleanFlowOnlyFlags), extra...)...)
}

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
