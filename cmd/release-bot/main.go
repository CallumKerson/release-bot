// Command release-bot releases packages from conventional commits.
// It tags merged releases, and otherwise prepares a release branch with the next versions and changelogs.
package main

import (
	"os"
	"time"

	"github.com/spf13/cobra"
)

func main() {
	if err := NewRootCommand(time.Now).Execute(); err != nil {
		os.Exit(1)
	}
}

// NewRootCommand builds the cobra command tree. now is the clock used for release dates and calendar versions.
func NewRootCommand(now func() time.Time) *cobra.Command {
	opts := &options{now: now}
	rootCmd := &cobra.Command{
		Use:          "release-bot",
		Short:        "releases packages from conventional commits",
		SilenceUsage: true,
		Version:      Version,
	}
	rootCmd.PersistentFlags().StringVarP(&opts.repo, "repo", "C", ".", "path inside the repository to release")
	rootCmd.PersistentFlags().StringVar(&opts.config, "config", "",
		"config file (default release-bot.toml or .config/release-bot.toml in the repository root)")
	rootCmd.PersistentFlags().BoolVar(&opts.json, "json", false, "print the result as JSON")

	rootCmd.AddCommand(NewVersionCommand())
	rootCmd.AddCommand(NewPlanCommand(opts))
	rootCmd.AddCommand(NewRunCommand(opts))
	return rootCmd
}
