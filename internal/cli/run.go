package cli

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/CallumKerson/release-bot/internal/config"
	"github.com/CallumKerson/release-bot/internal/git"
	"github.com/CallumKerson/release-bot/internal/github"
	"github.com/CallumKerson/release-bot/internal/release"
	"github.com/CallumKerson/release-bot/internal/vcs"
)

// options are the flags shared by every command.
type options struct {
	repo   string
	config string
	json   bool
	now    func() time.Time
	getenv func(string) string
}

// githubOptions are the flags for working on a GitHub repository through its API, instead of a checkout.
type githubOptions struct {
	enabled    bool
	repository string
	apiURL     string
	branch     string
}

// addGitHubFlags adds the flags that put a command on GitHub. what says what --github does beyond reading.
func addGitHubFlags(cmd *cobra.Command, githubOpts *githubOptions, what string) {
	cmd.Flags().BoolVar(&githubOpts.enabled, "github", false,
		"work on the GitHub repository through its API instead of a checkout"+what)
	cmd.Flags().StringVar(&githubOpts.repository, "github-repo", "",
		"the GitHub repository as owner/name, for --github (default $GITHUB_REPOSITORY)")
	cmd.Flags().StringVar(&githubOpts.apiURL, "github-api-url", "",
		"the GitHub REST API URL, for --github (default $GITHUB_API_URL, or https://api.github.com)")
	cmd.Flags().StringVar(&githubOpts.branch, "target-branch", "",
		"the branch to release from, for --github (default the repository's default branch)")
}

func newPlanCommand(opts *options) *cobra.Command {
	var githubOpts githubOptions
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Explain what run would do, and why, without changing anything",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, _, result, err := prepare(cmd.Context(), opts, &githubOpts)
			if err != nil {
				return err
			}
			return output(cmd.OutOrStdout(), opts, result, func(out io.Writer) { printPlan(out, result) })
		},
	}
	addGitHubFlags(cmd, &githubOpts, "")
	return cmd
}

func newRunCommand(opts *options) *cobra.Command {
	var dryRun bool
	var githubOpts githubOptions
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Tag merged releases, or rebuild the release branch when there are releasable commits",
		Long: `Run does one of three things to the repository:

  - tags the release commits that have been merged since the last run,
  - rebuilds the release branch from HEAD, when there are releasable commits since the last release,
  - or nothing, when there are no releasable commits.

Without --github, it works on the local repository, and never changes its working tree, its index or
the checked out branch.

With --github, it works on the GitHub repository through its API alone, so it needs no checkout:
it reads the config and history from the target branch, and creates the tags and the release branch
on GitHub. GitHub signs the release commit when the token belongs to a bot, such as GitHub Actions.
It then publishes a GitHub release of each current version that doesn't have one, and opens or
updates the release pull request.
It reads the token from GITHUB_TOKEN, and the repository and API URL from GITHUB_REPOSITORY and
GITHUB_API_URL unless the flags set them, as they are in GitHub Actions.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo, host, result, err := prepare(cmd.Context(), opts, &githubOpts)
			if err != nil {
				return err
			}
			run := &runOutput{Result: result, github: host != nil}
			if !dryRun {
				if run.Outcome, err = release.Apply(cmd.Context(), repo, result); err != nil {
					return err
				}
			}
			if !dryRun && host != nil {
				if run.Published, err = release.Publish(cmd.Context(), host, result); err != nil {
					return fmt.Errorf("publishing to GitHub: %w", err)
				}
			}
			return output(cmd.OutOrStdout(), opts, run, func(out io.Writer) { printRun(out, run) })
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print what would be done without doing it")
	addGitHubFlags(cmd, &githubOpts, ", and publish the release pull request and releases there")
	return cmd
}

// prepare works out what a run does, on GitHub with --github and otherwise on the local repository.
// host is the GitHub repository, or nil without --github.
func prepare(ctx context.Context, opts *options, githubOpts *githubOptions) (
	repo release.Repo, host *github.Host, result *release.Result, err error,
) {
	var cfg config.Config
	if githubOpts.enabled {
		if host, err = newGitHub(opts, githubOpts); err != nil {
			return nil, nil, nil, err
		}
		repo = host
		cfg, err = configOnGitHub(ctx, host, opts.config)
	} else {
		repo, cfg, err = openLocal(ctx, opts)
	}
	if err != nil {
		return nil, nil, nil, err
	}
	result, err = release.Prepare(ctx, repo, &cfg, opts.now().UTC())
	return repo, host, result, err
}

// newGitHub returns the GitHub repository to work on, failing before anything is read or written when it can't.
func newGitHub(opts *options, githubOpts *githubOptions) (*github.Host, error) {
	repository := cmp.Or(githubOpts.repository, opts.getenv("GITHUB_REPOSITORY"))
	if repository == "" {
		return nil, fmt.Errorf("%w: no repository, set GITHUB_REPOSITORY or --github-repo", github.ErrSettings)
	}
	return github.New(github.Options{
		Token:      opts.getenv("GITHUB_TOKEN"),
		Repository: repository,
		APIURL:     cmp.Or(githubOpts.apiURL, opts.getenv("GITHUB_API_URL")),
		Branch:     githubOpts.branch,
	})
}

// configOnGitHub reads the config from the head of the target branch: the file --config names,
// or the first of the paths it is looked for in.
func configOnGitHub(ctx context.Context, host *github.Host, explicit string) (config.Config, error) {
	head, err := host.Head(ctx)
	if err != nil {
		return config.Config{}, err
	}
	candidates := config.SearchPaths
	if explicit != "" {
		candidates = []string{explicit}
	}
	for _, path := range candidates {
		data, found, err := host.ReadFile(ctx, head, path)
		if err != nil {
			return config.Config{}, err
		}
		if !found {
			continue
		}
		cfg, err := config.Parse(data)
		if err != nil {
			return config.Config{}, fmt.Errorf("%s: %w", path, err)
		}
		return cfg, nil
	}
	return config.Config{}, fmt.Errorf("%w on GitHub at %s: create one of %s",
		config.ErrNotFound, vcs.Short(head), strings.Join(candidates, " or "))
}

// openLocal opens the local repository and loads its config.
func openLocal(ctx context.Context, opts *options) (*git.Repo, config.Config, error) {
	repo, err := git.Open(ctx, opts.repo)
	if err != nil {
		return nil, config.Config{}, err
	}
	file, err := config.Find(repo.Root(), opts.config)
	if err != nil {
		return nil, config.Config{}, err
	}
	cfg, err := config.Load(file)
	return repo, cfg, err
}

// runOutput is what run prints as JSON. Outcome and Published are nil for a dry run.
type runOutput struct {
	Result    *release.Result    `json:"result"`
	Outcome   *release.Outcome   `json:"outcome,omitempty"`
	Published *release.Published `json:"published,omitempty"`
	// github is whether the run publishes to GitHub.
	github bool
}

// output prints value as JSON with --json, and otherwise prints text.
func output(w io.Writer, opts *options, value any, text func(io.Writer)) error {
	if !opts.json {
		text(w)
		return nil
	}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return fmt.Errorf("writing JSON: %w", err)
	}
	return nil
}
