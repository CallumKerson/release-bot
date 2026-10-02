package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/CallumKerson/release-bot/internal/config"
	"github.com/CallumKerson/release-bot/internal/git"
	"github.com/CallumKerson/release-bot/internal/release"
)

// options are the flags shared by every command.
type options struct {
	repo   string
	config string
	json   bool
	now    func() time.Time
}

func NewPlanCommand(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "plan",
		Short: "Explain what run would do, and why, without changing anything",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, result, err := prepare(cmd.Context(), opts)
			if err != nil {
				return err
			}
			return output(cmd.OutOrStdout(), opts, result, printPlan)
		},
	}
}

func NewRunCommand(opts *options) *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Tag merged releases, or rebuild the release branch when there are releasable commits",
		Long: `Run does one of three things to the local repository:

  - tags the release commits that have been merged since the last run,
  - rebuilds the release branch from HEAD, when there are releasable commits since the last release,
  - or nothing, when there are no releasable commits.

It never changes the working tree, the index or the checked out branch.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo, result, err := prepare(cmd.Context(), opts)
			if err != nil {
				return err
			}
			if !dryRun {
				if err := release.Apply(cmd.Context(), repo, result); err != nil {
					return err
				}
			}
			return output(cmd.OutOrStdout(), opts, result, printRun)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print what would be done without doing it")
	return cmd
}

func prepare(ctx context.Context, opts *options) (*git.Repo, *release.Result, error) {
	repo, err := git.Open(ctx, opts.repo)
	if err != nil {
		return nil, nil, err
	}
	file, err := config.Find(repo.Root(), opts.config)
	if err != nil {
		return nil, nil, err
	}
	cfg, err := config.Load(file)
	if err != nil {
		return nil, nil, err
	}
	result, err := release.Prepare(ctx, repo, &cfg, opts.now().UTC())
	return repo, result, err
}

func output(w io.Writer, opts *options, result *release.Result, text func(io.Writer, *release.Result)) error {
	if !opts.json {
		text(w, result)
		return nil
	}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		return fmt.Errorf("writing JSON: %w", err)
	}
	return nil
}
