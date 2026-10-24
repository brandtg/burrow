package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
)

// manPageDate is the fixed man-page date. It is not "today" so the generated
// pages stay byte-for-byte reproducible for the CI freshness check.
var manPageDate = time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)

// newDocsCmd generates the man pages (or markdown) for every command. The
// output is generated, never hand-edited; scripts/check.sh regenerates it and
// fails if the committed pages are stale.
func (a *App) newDocsCmd(root *cobra.Command) *cobra.Command {
	var dir string
	var format string
	cmd := &cobra.Command{
		Use:   "docs",
		Short: "generate man pages for every command",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
			// A fixed tag keeps generated output reproducible so the CI
			// freshness check can diff it byte for byte.
			disableAutoGenTag(root)
			switch format {
			case "man":
				header := &doc.GenManHeader{
					Title:   "BURROW",
					Section: "1",
					Source:  "burrow " + a.Version,
					Manual:  "burrow manual",
					// A fixed date keeps the generated pages reproducible so
					// scripts/check.sh can diff them without a monthly false
					// positive. Bump it only when regenerating intentionally.
					Date: &manPageDate,
				}
				return doc.GenManTree(root, header, dir)
			case "markdown":
				return doc.GenMarkdownTree(root, dir)
			default:
				return fmt.Errorf("unknown docs format %q", format)
			}
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "docs/man", "output directory")
	cmd.Flags().StringVar(&format, "format", "man", "output format: man|markdown")
	return cmd
}

func disableAutoGenTag(cmd *cobra.Command) {
	cmd.DisableAutoGenTag = true
	for _, c := range cmd.Commands() {
		disableAutoGenTag(c)
	}
}
