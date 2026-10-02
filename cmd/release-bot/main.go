// Command release-bot releases packages from conventional commits.
// It tags merged releases, and otherwise prepares a release branch with the next versions and changelogs.
package main

import (
	"os"

	"github.com/spf13/cobra"
)

func main() {
	if err := NewRootCommand().Execute(); err != nil {
		os.Exit(1)
	}
}

// NewRootCommand builds the cobra command tree.
func NewRootCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:          "release-bot",
		Short:        "releases packages from conventional commits",
		SilenceUsage: true,
		Version:      Version,
	}

	rootCmd.AddCommand(NewVersionCommand())
	return rootCmd
}
