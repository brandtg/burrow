package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

func (a *App) newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "print the burrow version",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if a.Flags.JSON {
				return json.NewEncoder(a.Out).Encode(map[string]string{"version": a.Version})
			}
			fmt.Fprintf(a.Out, "burrow %s\n", a.Version)
			return nil
		},
	}
}
