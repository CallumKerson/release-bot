package release_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CallumKerson/release-bot/internal/config"
	"github.com/CallumKerson/release-bot/internal/manifest"
	"github.com/CallumKerson/release-bot/internal/release"
	"github.com/CallumKerson/release-bot/internal/release/releasetest"
)

// run prepares, applies and publishes, as release-bot run --github does.
func run(t *testing.T, repo *releasetest.Fake, host *releasetest.FakeHost) *release.Published {
	t.Helper()
	result := prepare(t, repo)
	outcome, err := release.Apply(t.Context(), repo, result)
	require.NoError(t, err)
	published, err := release.Publish(t.Context(), repo, host, result, outcome)
	require.NoError(t, err)
	return published
}

func TestPublishOpensTheReleasePullRequest(t *testing.T) {
	repo := releasetest.NewFake(
		released(t, "chore: release app 1.0.0", manifest.Manifest{"app": "1.0.0"}, "app-v1.0.0"),
		change("feat: add a thing", "app/main.go"),
	)
	host := releasetest.NewFakeHost(repo)

	published := run(t, repo, host)
	branch, _ := repo.Branch(config.DefaultBranch)
	pushed, _ := repo.Pushed("refs/heads/" + config.DefaultBranch)
	assert.Equal(t, branch, pushed)
	assert.True(t, published.BranchPushed)
	assert.Equal(t, &release.PullRequestResult{
		Number: 1, URL: "https://example.test/pull/1", Action: release.PullRequestOpened,
	}, published.PullRequest)

	pulls := host.PullRequests()
	require.Len(t, pulls, 1)
	assert.Equal(t, "chore(release): app 1.1.0", pulls[0].Title)
	assert.Contains(t, pulls[0].Body, "## app 1.1.0")
}

func TestPublishReleasesMergedReleases(t *testing.T) {
	repo := releasetest.NewFake(
		released(t, "chore: release app 1.0.0", manifest.Manifest{"app": "1.0.0"}, "app-v1.0.0"),
		change("feat: add a thing", "app/main.go"),
		released(t, "chore(release): app 1.1.0", manifest.Manifest{"app": "1.1.0"}),
	)
	host := releasetest.NewFakeHost(repo)

	published := run(t, repo, host)
	tagged := repo.Tags()["app-v1.1.0"]
	pushed, _ := repo.Pushed("refs/tags/app-v1.1.0")
	assert.Equal(t, tagged, pushed, "the tag is pushed")
	assert.Equal(t, []release.PublishedRelease{
		{Tag: "app-v1.1.0", URL: "https://example.test/releases/app-v1.1.0", Created: true},
	}, published.Releases)
	assert.Equal(t, []releasetest.Release{{Tag: "app-v1.1.0", Notes: "Release 1.1.0."}}, host.Releases())
	assert.Nil(t, published.PullRequest, "nothing new to release")
}

func TestPublishIsIdempotent(t *testing.T) {
	repo := releasetest.NewFake(
		released(t, "chore: release app 1.0.0", manifest.Manifest{"app": "1.0.0"}),
		change("feat: add a thing", "app/main.go"),
	)
	host := releasetest.NewFakeHost(repo)
	run(t, repo, host)
	writes := host.Writes()

	again := run(t, repo, host)
	assert.Equal(t, writes, host.Writes(), "nothing changes on the host")
	assert.False(t, again.BranchPushed)
	assert.Equal(t, release.PullRequestUnchanged, again.PullRequest.Action)
	assert.False(t, again.Releases[0].Created)
}

func TestPublishFinishesARunThatStoppedAfterTagging(t *testing.T) {
	repo := releasetest.NewFake(
		released(t, "chore: release app 1.0.0", manifest.Manifest{"app": "1.0.0"}, "app-v1.0.0"),
	)
	host := releasetest.NewFakeHost(repo)
	require.NoError(t, repo.PushTags(t.Context(), []string{"app-v1.0.0"}))

	published := run(t, repo, host)
	assert.Equal(t, []releasetest.Release{{Tag: "app-v1.0.0", Notes: "Release 1.0.0."}}, host.Releases(),
		"the tagged release gets its missing release")
	assert.True(t, published.Releases[0].Created)
}

func TestPublishReopensAPullRequestForAnUnchangedBranch(t *testing.T) {
	repo := releasetest.NewFake(
		released(t, "chore: release app 1.0.0", manifest.Manifest{"app": "1.0.0"}, "app-v1.0.0"),
		change("feat: add a thing", "app/main.go"),
	)
	host := releasetest.NewFakeHost(repo)
	run(t, repo, host)
	host.Merge(1)

	again := run(t, repo, host)
	assert.False(t, again.BranchPushed)
	assert.Equal(t, release.PullRequestOpened, again.PullRequest.Action, "a closed pull request is replaced")
	assert.Equal(t, 2, again.PullRequest.Number)
}
