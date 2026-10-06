package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/brandtg/burrow/internal/config"
	"github.com/brandtg/burrow/internal/engine"
	"github.com/brandtg/burrow/internal/journal"
	"github.com/brandtg/burrow/internal/trash"
	"github.com/spf13/cobra"
)

func (a *App) newHistoryCmd() *cobra.Command {
	var runID string
	var restore bool
	cmd := &cobra.Command{
		Use:   "history",
		Short: "read the operation journal",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			env, err := a.Env()
			if err != nil {
				return err
			}
			if restore {
				if runID == "" {
					return &ExitError{Code: ExitUsage, Err: errors.New("--restore requires --run <id>")}
				}
				return a.restoreRun(env, runID)
			}
			return a.printHistory(env, runID)
		},
	}
	cmd.Flags().StringVar(&runID, "run", "", "filter to a single run id")
	cmd.Flags().BoolVar(&restore, "restore", false, "restore trash items from a run (requires --run)")
	return cmd
}

func (a *App) printHistory(env *engine.Env, runID string) error {
	records, err := journal.Read(env.Paths.Journal)
	if err != nil {
		return err
	}
	if runID != "" {
		filtered := records[:0:0]
		for _, r := range records {
			if r.RunID == runID {
				filtered = append(filtered, r)
			}
		}
		records = filtered
	}
	if a.Flags.JSON {
		enc := json.NewEncoder(a.Out)
		for _, r := range records {
			if err := enc.Encode(r); err != nil {
				return err
			}
		}
		return nil
	}
	runs := journal.Group(records)
	if len(runs) == 0 {
		fmt.Fprintln(a.Out, "no operations recorded")
		return nil
	}
	tw := tabwriter.NewWriter(a.Out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TIME\tRUN\tCOMMAND\tOK\tSKIP\tFAIL\tFREED")
	for _, r := range runs {
		freed := config.FormatBytes(r.Freed)
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%d\t%s\n",
			r.Start.Local().Format(time.RFC3339), shortID(r.RunID), r.Command,
			r.OK, r.Skipped, r.Failed, freed)
	}
	return tw.Flush()
}

func (a *App) restoreRun(env *engine.Env, runID string) error {
	records, err := journal.Read(env.Paths.Journal)
	if err != nil {
		return err
	}
	run, ok := journal.Find(journal.Group(records), runID)
	if !ok {
		return fmt.Errorf("no run %q in journal", runID)
	}
	tr := trash.New(env.Paths.TrashDir, env.UID())
	entries, err := tr.List()
	if err != nil {
		return err
	}
	byPath := make(map[string]trash.Entry, len(entries))
	for _, e := range entries {
		byPath[e.Path] = e
	}
	restored := 0
	for _, rec := range journal.TrashItems(run) {
		entry, found := byPath[rec.Path]
		if !found {
			fmt.Fprintf(a.ErrOut, "skip %s: no longer in trash\n", rec.Path)
			continue
		}
		if err := tr.Restore(entry); err != nil {
			fmt.Fprintf(a.ErrOut, "skip %s: %v\n", rec.Path, err)
			continue
		}
		restored++
		fmt.Fprintf(a.Out, "restored %s\n", rec.Path)
	}
	if owners := journal.OwnerItems(run); len(owners) > 0 {
		fmt.Fprintf(a.Out, "%d owner-contract item(s) in this run are not restorable (owner-regenerable)\n", len(owners))
	}
	if restored == 0 {
		fmt.Fprintln(a.Out, "nothing restored")
	}
	return nil
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
