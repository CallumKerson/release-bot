package features

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/cucumber/godog"
)

// These steps put the scenario's repository on GitHub. The repository plays a developer's clone, with GitHub as its
// origin. release-bot runs with --github from an empty directory, with the environment a GitHub Actions workflow
// would give, as it works through GitHub's API alone and needs no checkout.
//
// GitHub is a fake one (fake_github_test.go), or in end-to-end mode a real one (real_github_test.go).
// The steps read branches and tags on it with git, and everything else through the gitHub interface.

// How a pull request is merged, as on GitHub's merge button and in its API.
const (
	mergeCommit = "merge"
	squash      = "squash"
	rebase      = "rebase"
)

// gitHub is a repository on a GitHub, fake or real, set up for one scenario.
type gitHub interface {
	// remote is the git URL of the repository, and the git config needed to fetch and push it.
	remote() (url string, config map[string]string)
	// env is the environment a workflow gives release-bot.
	env() map[string]string
	// publishRelease publishes a release of an existing tag, as someone using GitHub's website would.
	publishRelease(ctx context.Context, tag, notes string) error
	// mergePullRequest merges a pull request with a merge method, and returns the base branch's new commit.
	mergePullRequest(ctx context.Context, number int, method string) (string, error)
	// verified says whether GitHub signed a commit.
	verified(ctx context.Context, commit string) (bool, error)
	openPullRequests(ctx context.Context) ([]pullRequest, error)
	releases(ctx context.Context) ([]release, error)
	// activity describes everything done on GitHub except to branches and tags,
	// so that comparing it before and after a command shows whether the command did anything.
	activity(ctx context.Context) (string, error)
	// patience is how long the steps wait for GitHub's API to show what was just done, as a real GitHub can lag.
	patience() time.Duration
	// readable replaces what differs from one GitHub to another with what the fake GitHub says,
	// so scenarios read the same on both.
	readable(text string) string
	close() error
}

// pullRequest is the parts of a pull request the steps check.
type pullRequest struct {
	number      int
	title, body string
}

// release is the parts of a GitHub release the steps check.
type release struct {
	tag, body string
}

// onGitHub is the scenario's repository on GitHub.
type onGitHub struct {
	gitHub
	// nowhere is the empty directory release-bot runs in with --github.
	nowhere string
	// before is GitHub as it was before the last command ran.
	before gitHubSnapshot
}

type gitHubSnapshot struct {
	activity, refs string
	releases       []string
}

func registerGitHubSteps(scenario *godog.ScenarioContext, state *world) {
	scenario.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		if state.github == nil {
			return ctx, err
		}
		return ctx, errors.Join(err, state.github.close(), os.RemoveAll(state.github.nowhere))
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

// onGitHub puts the repository on GitHub, replacing whatever was there, and gives release-bot the environment of a
// workflow.
func (w *world) onGitHub(ctx context.Context) error {
	hub, err := w.newGitHub(ctx)
	if err != nil {
		return err
	}
	nowhere, err := os.MkdirTemp("", "release-bot-nowhere-")
	if err != nil {
		return errors.Join(err, hub.close())
	}
	w.github = &onGitHub{gitHub: hub, nowhere: nowhere}
	url, config := hub.remote()
	if _, err := w.git(ctx, nil, "remote", "add", "origin", url); err != nil {
		return err
	}
	for key, value := range config {
		if _, err := w.git(ctx, nil, "config", key, value); err != nil {
			return err
		}
	}
	maps.Copy(w.env, hub.env())
	_, err = w.git(ctx, nil, "push", "--quiet", "--force", "--prune", "origin",
		"+refs/heads/*:refs/heads/*", "+refs/tags/*:refs/tags/*")
	return err
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
		if err := w.github.publishRelease(ctx, tag, "Released before release-bot."); err != nil {
			return err
		}
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
	pull, err := w.openPullRequest(ctx)
	if err != nil {
		return err
	}
	merge := map[string]struct{ method, label string }{
		"a merge commit": {mergeCommit, "merge of " + w.cfg.Branch},
		"a squash merge": {squash, "squashed release"},
		"a rebase":       {rebase, "rebased release"},
	}[strategy]
	merged, err := w.github.mergePullRequest(ctx, pull.number, merge.method)
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
	commit, err := w.originRev(ctx, "refs/heads/"+w.cfg.Branch)
	if err != nil {
		return fmt.Errorf("the release branch is %w: GitHub doesn't have it, and release-bot said:\n%s",
			errNotAsExpected, w.output)
	}
	parent, err := w.git(ctx, nil, "rev-parse", commit+"^")
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
	verified, err := w.github.verified(ctx, commit)
	if err != nil {
		return err
	}
	if !verified {
		return fmt.Errorf("the release commit is %w: GitHub didn't sign it", errNotAsExpected)
	}
	return nil
}

func (w *world) pullRequestOpen(ctx context.Context, title string) error {
	return w.eventually(ctx, func() error {
		pull, err := w.openPullRequest(ctx)
		if err != nil {
			return err
		}
		return compare("the release pull request's title", title, pull.title)
	})
}

func (w *world) pullRequestSays(ctx context.Context, expected *godog.DocString) error {
	return w.eventually(ctx, func() error {
		pull, err := w.openPullRequest(ctx)
		if err != nil {
			return err
		}
		return compare("the release pull request's body", expected.Content, w.readable(pull.body))
	})
}

func (w *world) releasesPublished(ctx context.Context, expected *godog.Table) error {
	want := make([]string, 0, len(expected.Rows)-1)
	for _, r := range expected.Rows[1:] {
		want = append(want, r.Cells[0].Value+" on "+r.Cells[1].Value)
	}
	slices.Sort(want)
	return w.eventually(ctx, func() error {
		got, err := w.publishedReleases(ctx)
		if err != nil {
			return err
		}
		return compare("published releases", strings.Join(want, "\n"), strings.Join(got, "\n"))
	})
}

func (w *world) noReleasesPublished(ctx context.Context) error {
	got, err := w.publishedReleases(ctx)
	if err != nil {
		return err
	}
	return compare("published releases", "", strings.Join(got, "\n"))
}

func (w *world) releaseSays(ctx context.Context, tag string, expected *godog.DocString) error {
	return w.eventually(ctx, func() error {
		releases, err := w.github.releases(ctx)
		if err != nil {
			return err
		}
		for _, release := range releases {
			if release.tag == tag {
				return compare("the release of "+tag, expected.Content, w.readable(release.body))
			}
		}
		return fmt.Errorf("the release of %s is %w: there isn't one", tag, errNotAsExpected)
	})
}

func (w *world) nothingOnGitHubChanges(ctx context.Context) error {
	now, err := w.gitHubSnapshot(ctx)
	if err != nil {
		return err
	}
	if err := compare("activity on GitHub", w.github.before.activity, now.activity); err != nil {
		return err
	}
	return compare("branches and tags on GitHub", w.readable(w.github.before.refs), w.readable(now.refs))
}

// helpers

// eventually runs check until it passes, or GitHub's patience runs out, and returns its last error.
func (w *world) eventually(ctx context.Context, check func() error) error {
	deadline := time.Now().Add(w.github.patience())
	for {
		err := check()
		if err == nil || time.Now().After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(time.Second):
		}
	}
}

// openPullRequest returns the one open pull request.
func (w *world) openPullRequest(ctx context.Context) (pullRequest, error) {
	open, err := w.github.openPullRequests(ctx)
	if err != nil {
		return pullRequest{}, err
	}
	if len(open) != 1 {
		return pullRequest{}, fmt.Errorf("the release pull request is %w: there are %d open pull requests, "+
			"and release-bot said:\n%s", errNotAsExpected, len(open), w.output)
	}
	return open[0], nil
}

// publishedReleases lists the releases published by the last command, as "tag on commit", sorted.
func (w *world) publishedReleases(ctx context.Context) ([]string, error) {
	releases, err := w.github.releases(ctx)
	if err != nil {
		return nil, err
	}
	var published []string
	for _, release := range releases {
		if slices.Contains(w.github.before.releases, release.tag) {
			continue
		}
		commit, err := w.originRev(ctx, "refs/tags/"+release.tag)
		if err != nil {
			return nil, err
		}
		published = append(published, release.tag+" on "+w.readable(commit))
	}
	slices.Sort(published)
	return published, nil
}

func (w *world) gitHubSnapshot(ctx context.Context) (gitHubSnapshot, error) {
	var snap gitHubSnapshot
	var err error
	if snap.activity, err = w.github.activity(ctx); err != nil {
		return snap, err
	}
	releases, err := w.github.releases(ctx)
	if err != nil {
		return snap, err
	}
	for _, release := range releases {
		snap.releases = append(snap.releases, release.tag)
	}
	snap.refs, err = w.git(ctx, nil, "ls-remote", "--heads", "--tags", "origin")
	return snap, err
}

// originRev returns the commit a branch or tag on GitHub points to, fetching it without changing any local ref,
// so that the steps checking the local repository can't tell.
func (w *world) originRev(ctx context.Context, ref string) (string, error) {
	if _, err := w.git(ctx, nil, "fetch", "--quiet", "--no-tags", "--refmap=", "origin", ref); err != nil {
		return "", err
	}
	return w.git(ctx, nil, "rev-parse", "--verify", "FETCH_HEAD^{commit}")
}
