package release_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CallumKerson/release-bot/internal/config"
	"github.com/CallumKerson/release-bot/internal/manifest"
	"github.com/CallumKerson/release-bot/internal/release"
	"github.com/CallumKerson/release-bot/internal/release/releasetest"
	"github.com/CallumKerson/release-bot/internal/vcs"
)

var now = time.Date(2026, time.October, 2, 9, 0, 0, 0, time.UTC)

func mustConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Parse([]byte("[packages.app]\npath = \"app\""))
	require.NoError(t, err)
	return &cfg
}

// released is a commit that writes versions to the manifest.
func released(t *testing.T, message string, versions manifest.Manifest, tags ...string) releasetest.Commit {
	t.Helper()
	data, err := versions.Marshal()
	require.NoError(t, err)
	return releasetest.Commit{
		Message: message,
		Files:   map[string]string{config.DefaultManifest: string(data)},
		Tags:    tags,
	}
}

func change(message, file string) releasetest.Commit {
	return releasetest.Commit{Message: message, Files: map[string]string{file: message + "\n"}}
}

func prepare(t *testing.T, repo release.Repo) *release.Result {
	t.Helper()
	result, err := release.Prepare(t.Context(), repo, mustConfig(t), now)
	require.NoError(t, err)
	return result
}

func TestNothingToRelease(t *testing.T) {
	repo := releasetest.NewFake(
		released(t, "chore: release app 1.0.0", manifest.Manifest{"app": "1.0.0"}, "app-v1.0.0"),
		change("docs: explain", "app/README.md"),
	)
	result := prepare(t, repo)
	assert.True(t, result.Nothing())
	assert.Empty(t, result.Untagged)
	assert.Nil(t, result.Branch)
}

func TestPrepareRendersTheReleaseBranch(t *testing.T) {
	repo := releasetest.NewFake(
		released(t, "chore: release app 1.0.0", manifest.Manifest{"app": "1.0.0"}, "app-v1.0.0"),
		change("feat: add a thing", "app/main.go"),
	)
	result := prepare(t, repo)
	require.NotNil(t, result.Branch)
	assert.Equal(t, config.DefaultBranch, result.Branch.Name)
	assert.Equal(t, "chore(release): app 1.1.0\n\n- app 1.0.0 -> 1.1.0\n", result.Branch.Message)
	assert.Equal(t, []string{config.DefaultManifest, "app/CHANGELOG.md"}, result.Branch.Paths())

	versions, err := manifest.Parse(result.Branch.Files[config.DefaultManifest])
	require.NoError(t, err)
	assert.Equal(t, manifest.Manifest{"app": "1.1.0"}, versions)
	assert.Contains(t, string(result.Branch.Files["app/CHANGELOG.md"]), "## 1.1.0 (2026-10-02)")
}

func TestPrepareFindsMergedReleasesToTag(t *testing.T) {
	repo := releasetest.NewFake(
		released(t, "chore: release app 1.0.0", manifest.Manifest{"app": "1.0.0"}, "app-v1.0.0"),
		change("feat: add a thing", "app/main.go"),
		released(t, "chore(release): app 1.1.0", manifest.Manifest{"app": "1.1.0"}),
		change("fix: a bug", "app/main.go"),
	)
	head, err := repo.Head(t.Context())
	require.NoError(t, err)
	log, err := repo.Log(t.Context(), "", head)
	require.NoError(t, err)

	result := prepare(t, repo)
	assert.Equal(t, []release.Tag{
		{Package: "app", Version: "1.1.0", Name: "app-v1.1.0", ReleaseCommit: log[1].SHA, Notes: "Release 1.1.0."},
	}, result.Untagged)
	require.Len(t, result.Plan.Packages, 1)
	assert.Equal(t, "1.1.1", result.Plan.Packages[0].Next, "only commits since the merged release count")
}

func TestPrepareTakesReleaseNotesFromTheChangelog(t *testing.T) {
	tagged := released(t, "chore: release app 1.0.0", manifest.Manifest{"app": "1.0.0"}, "app-v1.0.0")
	tagged.Files["app/CHANGELOG.md"] = "# Changelog\n\n## 1.0.0 (2026-09-01)\n\n### Features\n\n- first (aaa)\n"
	untagged := released(t, "chore(release): app 1.1.0", manifest.Manifest{"app": "1.1.0"})
	untagged.Files["app/CHANGELOG.md"] = "# Changelog\n\n## 1.1.0 (2026-10-01)\n\n### Bug Fixes\n\n- second (bbb)\n\n" +
		"## 1.0.0 (2026-09-01)\n\n### Features\n\n- first (aaa)\n"
	repo := releasetest.NewFake(tagged, untagged)

	result := prepare(t, repo)
	require.Len(t, result.Untagged, 1)
	assert.Equal(t, "### Bug Fixes\n\n- second (bbb)", result.Untagged[0].Notes)
	assert.Empty(t, result.Tagged, "the tagged release is no longer current")
}

func TestPrepareListsCurrentReleasesThatAreTagged(t *testing.T) {
	repo := releasetest.NewFake(
		released(t, "chore: release app 1.0.0", manifest.Manifest{"app": "1.0.0"}, "app-v1.0.0"),
	)
	head, err := repo.Head(t.Context())
	require.NoError(t, err)

	result := prepare(t, repo)
	assert.Equal(t, []release.Tag{
		{Package: "app", Version: "1.0.0", Name: "app-v1.0.0", ReleaseCommit: head, Notes: "Release 1.0.0."},
	}, result.Tagged)
	assert.True(t, result.Nothing(), "tagged releases need nothing doing locally")
}

func TestPrepareDescribesTheReleaseForAPullRequest(t *testing.T) {
	repo := releasetest.NewFake(
		released(t, "chore: release app 1.0.0", manifest.Manifest{"app": "1.0.0"}, "app-v1.0.0"),
		change("feat: add a thing", "app/main.go"),
	)
	head, err := repo.Head(t.Context())
	require.NoError(t, err)

	result := prepare(t, repo)
	require.NotNil(t, result.Branch)
	assert.Equal(t, "chore(release): app 1.1.0", result.Branch.Title)
	assert.Equal(t, "## app 1.1.0\n\n### Features\n\n- add a thing ("+vcs.Short(head)+")\n", result.Branch.Body)
}

func TestApplyTagsAndWritesTheBranch(t *testing.T) {
	repo := releasetest.NewFake(
		released(t, "chore: release app 1.0.0", manifest.Manifest{"app": "1.0.0"}),
		change("feat: add a thing", "app/main.go"),
	)
	result := prepare(t, repo)
	outcome, err := release.Apply(t.Context(), repo, result)
	require.NoError(t, err)

	assert.Contains(t, repo.Tags(), "app-v1.0.0")
	branch, ok := repo.Branch(config.DefaultBranch)
	require.True(t, ok)
	assert.Equal(t, &release.Outcome{BranchCommit: branch, BranchChanged: true}, outcome)

	again, err := release.Apply(t.Context(), repo, prepare(t, repo))
	require.NoError(t, err)
	assert.Equal(t, &release.Outcome{BranchCommit: branch, BranchChanged: false}, again, "runs are idempotent")
}

func TestManifestMustOnlyListReleasedPackages(t *testing.T) {
	repo := releasetest.NewFake(released(t, "chore: release", manifest.Manifest{"app": "1.0.0", "gone": "1.0.0"}))
	_, err := release.Prepare(t.Context(), repo, mustConfig(t), now)
	require.ErrorIs(t, err, manifest.ErrInvalid)
}

func TestBranchJSONListsPathsNotContents(t *testing.T) {
	branch := &release.Branch{
		Name:    "release-bot/release",
		Message: "chore(release): app 1.0.0",
		Title:   "chore(release): app 1.0.0",
		Body:    "## app 1.0.0",
		Files:   map[string][]byte{"b/CHANGELOG.md": []byte("b"), "a.json": []byte("a")},
	}
	data, err := json.Marshal(branch)
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"name": "release-bot/release",
		"message": "chore(release): app 1.0.0",
		"title": "chore(release): app 1.0.0",
		"body": "## app 1.0.0",
		"files": ["a.json", "b/CHANGELOG.md"]
	}`, string(data))
}
