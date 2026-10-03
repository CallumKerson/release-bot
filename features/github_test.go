package features

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cucumber/godog"

	"github.com/CallumKerson/release-bot/internal/github/githubtest"
)

// These steps put the scenario's repository on a fake GitHub. The repository plays a developer's clone, with the
// fake's git repository as its origin. release-bot runs with --github from an empty directory, with the environment
// a GitHub Actions workflow would give, as it works through GitHub's API alone and needs no checkout.

// repository is the fake GitHub repository every scenario uses.
const repository = "octo-org/widgets"

// gitHub is a scenario's fake GitHub.
type gitHub struct {
	server *githubtest.Server
	// origin is the bare git repository behind the fake, and the scenario repository's origin.
	origin string
	// nowhere is the empty directory release-bot runs in with --github.
	nowhere string
	// before is GitHub as it was before the last command ran.
	before gitHubSnapshot
}

type gitHubSnapshot struct {
	writes, releases int
	refs             string
}

func registerGitHubSteps(scenario *godog.ScenarioContext, state *world) {
	scenario.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		if state.github == nil {
			return ctx, err
		}
		return ctx, errors.Join(err, state.github.server.Close(), os.RemoveAll(filepath.Dir(state.github.origin)),
			os.RemoveAll(state.github.nowhere))
	})

	scenario.Step(`^the repository is on GitHub$`, state.onGitHub)
	scenario.Step(`^the repository is on GitHub, with a release of each tag$`, state.onGitHubWithReleases)
	scenario.Step(`^there is no GitHub token$`, state.noGitHubToken)
	scenario.Step(`^the tags are pushed to GitHub$`, state.tagsPushed)

	scenario.Step(
		`^the release pull request is merged with (a merge commit|a squash merge|a rebase)$`,
		state.pullRequestMerged,
	)

	scenario.Step(`^the release branch is on GitHub$`, state.releaseBranchOnGitHub)
	scenario.Step(`^the release commit is signed by GitHub$`, state.releaseCommitSigned)
	scenario.Step(`^the release pull request is open, titled "(.*)"$`, state.pullRequestOpen)
	scenario.Step(`^the release pull request says:$`, state.pullRequestSays)
	scenario.Step(`^these GitHub releases are published:$`, state.releasesPublished)
	scenario.Step(`^no GitHub releases are published$`, state.noReleasesPublished)
	scenario.Step(`^the GitHub release "([^"]+)" says:$`, state.releaseSays)
	scenario.Step(`^nothing on GitHub changes$`, state.nothingOnGitHubChanges)
}

// Given

// onGitHub pushes the repository to a new fake GitHub, and gives release-bot the environment of a workflow.
func (w *world) onGitHub(ctx context.Context) error {
	dir, err := os.MkdirTemp("", "release-bot-origin-")
	if err != nil {
		return err
	}
	origin := filepath.Join(dir, "origin.git")
	if _, err := w.git(ctx, nil, "init", "--quiet", "--bare", origin); err != nil {
		return err
	}
	if _, err := w.git(ctx, nil, "remote", "add", "origin", origin); err != nil {
		return err
	}
	nowhere, err := os.MkdirTemp("", "release-bot-nowhere-")
	if err != nil {
		return err
	}
	server, err := githubtest.Start(githubtest.Options{Repository: repository, Origin: origin})
	if err != nil {
		return err
	}
	w.github = &gitHub{server: server, origin: origin, nowhere: nowhere}
	w.env["GITHUB_TOKEN"] = githubtest.Token
	w.env["GITHUB_REPOSITORY"] = repository
	w.env["GITHUB_API_URL"] = server.URL
	return w.push(ctx)
}

func (w *world) onGitHubWithReleases(ctx context.Context) error {
	if err := w.onGitHub(ctx); err != nil {
		return err
	}
	tags, err := w.git(ctx, nil, "tag", "--list")
	if err != nil {
		return err
	}
	for tag := range strings.FieldsSeq(tags) {
		w.github.server.PublishRelease(tag, "Released before release-bot.")
	}
	return nil
}

func (w *world) noGitHubToken() {
	delete(w.env, "GITHUB_TOKEN")
}

func (w *world) tagsPushed(ctx context.Context) error {
	_, err := w.git(ctx, nil, "push", "--quiet", "--tags", "origin")
	return err
}

// push pushes main and every tag to GitHub, as when commits land on main there.
func (w *world) push(ctx context.Context) error {
	if w.github == nil {
		return nil
	}
	_, err := w.git(ctx, nil, "push", "--quiet", "--tags", "origin", "main")
	return err
}

// When

// pullRequestMerged merges the open release pull request on GitHub,
// then updates the repository to the new main, as the next workflow's checkout would be.
func (w *world) pullRequestMerged(ctx context.Context, strategy string) error {
	pull, err := w.openPullRequest()
	if err != nil {
		return err
	}
	merge := map[string]struct{ method, label string }{
		"a merge commit": {githubtest.MergeCommit, "merge of " + w.cfg.Branch},
		"a squash merge": {githubtest.Squash, "squashed release"},
		"a rebase":       {githubtest.Rebase, "rebased release"},
	}[strategy]
	merged, err := w.github.server.MergePullRequest(ctx, pull.number, merge.method)
	if err != nil {
		return err
	}
	if _, err := w.git(ctx, nil, "fetch", "--quiet", "--tags", "origin"); err != nil {
		return err
	}
	if _, err := w.git(ctx, nil, "reset", "--quiet", "--hard", "origin/main"); err != nil {
		return err
	}
	w.label(merge.label, merged)
	return nil
}

// Then

// releaseBranchOnGitHub checks GitHub has the release branch, one commit on top of main.
func (w *world) releaseBranchOnGitHub(ctx context.Context) error {
	if _, err := w.originRev(ctx, "refs/heads/"+w.cfg.Branch); err != nil {
		return fmt.Errorf("the release branch is %w: GitHub doesn't have it, and release-bot said:\n%s",
			errNotAsExpected, w.output)
	}
	parent, err := w.originRev(ctx, "refs/heads/"+w.cfg.Branch+"^")
	if err != nil {
		return err
	}
	main, err := w.originRev(ctx, "refs/heads/main")
	if err != nil {
		return err
	}
	return compare("the parent of the release commit on GitHub", w.readable(main), w.readable(parent))
}

func (w *world) releaseCommitSigned(ctx context.Context) error {
	commit, err := w.originRev(ctx, "refs/heads/"+w.cfg.Branch)
	if err != nil {
		return err
	}
	if !w.github.server.Verified(commit) {
		return fmt.Errorf("the release commit is %w: GitHub didn't sign it", errNotAsExpected)
	}
	return nil
}

func (w *world) pullRequestOpen(title string) error {
	pull, err := w.openPullRequest()
	if err != nil {
		return err
	}
	return compare("the release pull request's title", title, pull.title)
}

func (w *world) pullRequestSays(expected *godog.DocString) error {
	pull, err := w.openPullRequest()
	if err != nil {
		return err
	}
	return compare("the release pull request's body", expected.Content, w.readable(pull.body))
}

func (w *world) releasesPublished(ctx context.Context, expected *godog.Table) error {
	got, err := w.publishedReleases(ctx)
	if err != nil {
		return err
	}
	want := make([]string, 0, len(expected.Rows)-1)
	for _, r := range expected.Rows[1:] {
		want = append(want, r.Cells[0].Value+" on "+r.Cells[1].Value)
	}
	slices.Sort(want)
	return compare("published releases", strings.Join(want, "\n"), strings.Join(got, "\n"))
}

func (w *world) noReleasesPublished(ctx context.Context) error {
	got, err := w.publishedReleases(ctx)
	if err != nil {
		return err
	}
	return compare("published releases", "", strings.Join(got, "\n"))
}

func (w *world) releaseSays(tag string, expected *godog.DocString) error {
	for _, release := range w.github.server.Releases() {
		if release.TagName == tag {
			return compare("the release of "+tag, expected.Content, w.readable(release.GetBody()))
		}
	}
	return fmt.Errorf("the release of %s is %w: there isn't one", tag, errNotAsExpected)
}

func (w *world) nothingOnGitHubChanges(ctx context.Context) error {
	now, err := w.gitHubSnapshot(ctx)
	if err != nil {
		return err
	}
	if writes := w.github.server.Writes()[w.github.before.writes:]; len(writes) > 0 {
		return fmt.Errorf("GitHub is %w: release-bot made %d changes, starting with %s %s",
			errNotAsExpected, len(writes), writes[0].Method, writes[0].Path)
	}
	return compare("branches and tags on GitHub", w.readable(w.github.before.refs), w.readable(now.refs))
}

// helpers

// pullRequest is the parts of a pull request the steps check.
type pullRequest struct {
	number      int
	title, body string
}

// openPullRequest returns the one open pull request.
func (w *world) openPullRequest() (pullRequest, error) {
	var open []pullRequest
	for _, pull := range w.github.server.PullRequests() {
		if pull.GetState() == "open" {
			open = append(open, pullRequest{pull.GetNumber(), pull.GetTitle(), pull.GetBody()})
		}
	}
	if len(open) != 1 {
		return pullRequest{}, fmt.Errorf("the release pull request is %w: there are %d open pull requests, "+
			"and release-bot said:\n%s", errNotAsExpected, len(open), w.output)
	}
	return open[0], nil
}

// publishedReleases lists the releases published by the last command, as "tag on commit", sorted.
func (w *world) publishedReleases(ctx context.Context) ([]string, error) {
	var published []string
	for _, release := range w.github.server.Releases()[w.github.before.releases:] {
		commit, err := w.originRev(ctx, "refs/tags/"+release.TagName)
		if err != nil {
			return nil, err
		}
		published = append(published, release.TagName+" on "+w.readable(commit))
	}
	slices.Sort(published)
	return published, nil
}

func (w *world) gitHubSnapshot(ctx context.Context) (gitHubSnapshot, error) {
	refs, err := w.git(ctx, nil, "--git-dir", w.github.origin, "for-each-ref", "--format=%(refname) %(objectname)")
	return gitHubSnapshot{
		writes:   len(w.github.server.Writes()),
		releases: len(w.github.server.Releases()),
		refs:     refs,
	}, err
}

// originRev returns the commit a ref points to on GitHub.
func (w *world) originRev(ctx context.Context, ref string) (string, error) {
	return w.git(ctx, nil, "--git-dir", w.github.origin, "rev-parse", "--verify", ref+"^{commit}")
}
