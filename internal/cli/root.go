// Package cli is release-bot's command line: the plan, run and version commands, and their output.
package cli

import (
	"time"

	"github.com/spf13/cobra"
)

// NewRootCommand builds the cobra command tree. now is the clock used for release dates and calendar versions,
// and getenv looks up environment variables, such as the GitHub token.
func NewRootCommand(now func() time.Time, getenv func(string) string) *cobra.Command {
	opts := &options{now: now, getenv: getenv}
	rootCmd := &cobra.Command{
		Use:          "release-bot",
		Short:        "releases packages from conventional commits",
		SilenceUsage: true,
		Version:      Version,
	}
	rootCmd.PersistentFlags().
		StringVarP(&opts.repo, "repo", "C", ".", "path inside the local repository to release, without --github")
	rootCmd.PersistentFlags().StringVar(&opts.config, "config", "",
		"config file, a path inside the repository with --github "+
			"(default release-bot.toml or .config/release-bot.toml in the repository root)")
	rootCmd.PersistentFlags().BoolVar(&opts.json, "json", false, "print the result as JSON")

	rootCmd.AddCommand(newVersionCommand())
	rootCmd.AddCommand(newPlanCommand(opts))
	rootCmd.AddCommand(newRunCommand(opts))
	return rootCmd
}
