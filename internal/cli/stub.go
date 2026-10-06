package cli

import (
	"errors"

	"github.com/brandtg/burrow/internal/i18n"
	"github.com/spf13/cobra"
)

// newStubCmd registers a command whose implementation lands in a later plan.
// It returns a non-zero usage code and a clear message so the surface exists
// without pretending to work.
func (a *App) newStubCmd(name, short string) *cobra.Command {
	return &cobra.Command{
		Use:   name,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			return &ExitError{Code: ExitUsage, Err: errors.New(i18n.Tf(i18n.ErrNotImpl, name))}
		},
	}
}
