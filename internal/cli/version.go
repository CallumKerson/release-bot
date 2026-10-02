package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Build information, set with -ldflags "-X" at build time.
var (
	Version = "development"
	Commit  = "development"
	Date    = "development"
)

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version and build information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintln(cmd.OutOrStdout(), "version: ", Version)
			fmt.Fprintln(cmd.OutOrStdout(), "commit:  ", Commit)
			fmt.Fprintln(cmd.OutOrStdout(), "built at:", Date)
		},
	}
}
