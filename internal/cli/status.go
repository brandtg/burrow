package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"text/tabwriter"

	"github.com/brandtg/burrow/internal/config"
	"github.com/brandtg/burrow/internal/metrics"
	"github.com/spf13/cobra"
)

// newStatusCmd implements the thin, one-shot system snapshot (spec §11, §20.5).
// It is explicitly not the product differentiator: it renders once and exits.
func (a *App) newStatusCmd() *cobra.Command {
	var root string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "one-shot system snapshot (thin)",
		Long: `Print a one-shot snapshot of CPU, memory, disk, network, and processes.

This is intentionally thin and read-only; it is not a monitor and starts no
daemon. With --json it emits a stable schema.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runStatus(root)
		},
	}
	cmd.Flags().StringVar(&root, "path", "/", "filesystem root to report disk usage for")
	return cmd
}

func (a *App) runStatus(root string) error {
	if root == "" {
		root = "/"
	}
	collect := a.CollectMetrics
	if collect == nil {
		collect = metrics.Collect
	}
	snap, err := collect(context.Background(), root)
	if err != nil {
		fmt.Fprintf(a.ErrOut, "warning: some metrics unavailable: %v\n", err)
	}
	if a.Flags.JSON {
		enc := json.NewEncoder(a.Out)
		enc.SetIndent("", "  ")
		return enc.Encode(snap)
	}
	tw := tabwriter.NewWriter(a.Out, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "cpu\t%.1f%%\n", snap.CPUPercent)
	fmt.Fprintf(tw, "memory\t%s / %s (%.1f%%)\n",
		config.FormatBytes(int64(snap.MemUsed)), config.FormatBytes(int64(snap.MemTotal)), snap.MemPercent)
	fmt.Fprintf(tw, "disk %s\t%s / %s (%.1f%%), %s free\n", snap.DiskPath,
		config.FormatBytes(int64(snap.DiskUsed)), config.FormatBytes(int64(snap.DiskTotal)),
		snap.DiskPercent, config.FormatBytes(int64(snap.DiskFree)))
	fmt.Fprintf(tw, "network\t%s rx / %s tx\n",
		config.FormatBytes(int64(snap.NetRxBytes)), config.FormatBytes(int64(snap.NetTxBytes)))
	fmt.Fprintf(tw, "processes\t%d\n", snap.ProcessCount)
	if err := tw.Flush(); err != nil {
		return err
	}
	if len(snap.TopProcesses) == 0 {
		return nil
	}
	fmt.Fprintln(a.Out, "\ntop processes by memory:")
	tw2 := tabwriter.NewWriter(a.Out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw2, "PID\tNAME\tRSS\tCPU")
	for _, p := range snap.TopProcesses {
		fmt.Fprintf(tw2, "%d\t%s\t%s\t%.1f%%\n", p.PID, p.Name, config.FormatBytes(int64(p.RSS)), p.CPUPercent)
	}
	return tw2.Flush()
}
