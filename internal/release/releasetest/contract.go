package releasetest

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CallumKerson/release-bot/internal/release"
	"github.com/CallumKerson/release-bot/internal/vcs"
)

const (
	readme    = "README.md"
	appMain   = "app/main.go"
	changelog = "CHANGELOG.md"
	firstTag  = "v1.0.0"
)

// Builder returns a repository whose checked out branch, appMain, has history, oldest commit first.
type Builder func(t *testing.T, history ...Commit) release.Repo

// contractHistory is the history every contract test starts from. Its newest commit changes no files.
var contractHistory = []Commit{
	{Message: "chore: init", Files: map[string]string{readme: "hello\n", appMain: "v1\n"}},
	{Message: "feat(app): add a thing\n\nWith a body.", Files: map[string]string{appMain: "v2\n"}},
	{Message: "chore: empty", Tags: []string{firstTag}},
}

// RunContract tests that the repositories build makes behave as release.Prepare and release.Apply expect.
func RunContract(t *testing.T, build Builder) {
	t.Helper()
	for name, test := range map[string]func(*testing.T, release.Repo, []vcs.Commit){
		"Log":         testLog,
		"ReadFile":    testReadFile,
		"FileHistory": testFileHistory,
		"Tags":        testTags,
		"WriteBranch": testWriteBranch,
	} {
		t.Run(name, func(t *testing.T) {
			repo := build(t, contractHistory...)
			head, err := repo.Head(t.Context())
			require.NoError(t, err)
			log, err := repo.Log(t.Context(), "", head)
			require.NoError(t, err)
			require.Len(t, log, len(contractHistory))
			require.Equal(t, head, log[0].SHA, "newest first")
			test(t, repo, log)
		})
	}
}

func testLog(t *testing.T, repo release.Repo, log []vcs.Commit) {
	t.Helper()
	assert.Equal(t, "chore: empty", log[0].Message)
	assert.Empty(t, log[0].Files)
	assert.Equal(t, "feat(app): add a thing\n\nWith a body.", log[1].Message)
	assert.Equal(t, []string{appMain}, log[1].Files)
	assert.ElementsMatch(t, []string{readme, appMain}, log[2].Files)

	since, err := repo.Log(t.Context(), log[2].SHA, log[0].SHA)
	require.NoError(t, err)
	assert.Equal(t, log[:2], since)
}

func testReadFile(t *testing.T, repo release.Repo, log []vcs.Commit) {
	t.Helper()
	head := log[0].SHA
	content, found, err := repo.ReadFile(t.Context(), head, appMain)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "v2\n", string(content))

	content, found, err = repo.ReadFile(t.Context(), head+"^^", appMain)
	require.NoError(t, err)
	assert.True(t, found, "a parent's parent")
	assert.Equal(t, "v1\n", string(content))

	_, found, err = repo.ReadFile(t.Context(), head, "missing.txt")
	require.NoError(t, err)
	assert.False(t, found, "a missing file")

	_, found, err = repo.ReadFile(t.Context(), head+"^^^", readme)
	require.NoError(t, err)
	assert.False(t, found, "the parent of the first commit")
}

func testFileHistory(t *testing.T, repo release.Repo, log []vcs.Commit) {
	t.Helper()
	changes, err := repo.FileHistory(t.Context(), log[0].SHA, appMain)
	require.NoError(t, err)
	assert.Equal(t, []string{log[1].SHA, log[2].SHA}, changes)
}

func testTags(t *testing.T, repo release.Repo, log []vcs.Commit) {
	t.Helper()
	commit, found, err := repo.TagCommit(t.Context(), firstTag)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, log[0].SHA, commit)

	_, found, err = repo.TagCommit(t.Context(), "v2.0.0")
	require.NoError(t, err)
	assert.False(t, found, "a missing tag")

	require.NoError(t, repo.CreateTag(t.Context(), "v2.0.0", log[1].SHA, "app 2.0.0"))
	commit, found, err = repo.TagCommit(t.Context(), "v2.0.0")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, log[1].SHA, commit)

	require.Error(t, repo.CreateTag(t.Context(), "v2.0.0", log[0].SHA, "again"), "an existing tag")
}

func testWriteBranch(t *testing.T, repo release.Repo, log []vcs.Commit) {
	t.Helper()
	head := log[0].SHA
	files := map[string][]byte{changelog: []byte("# Changelog\n"), appMain: []byte("v3\n")}
	const message = "chore(release): app 1.1.0"

	commit, changed, err := repo.WriteBranch(t.Context(), "release", head, files, message)
	require.NoError(t, err)
	assert.True(t, changed)
	released, err := repo.Log(t.Context(), head, commit)
	require.NoError(t, err)
	require.Len(t, released, 1, "one commit on top of the parent")
	assert.Equal(t, message, released[0].Message)
	assert.ElementsMatch(t, []string{changelog, appMain}, released[0].Files)
	content, _, err := repo.ReadFile(t.Context(), commit, readme)
	require.NoError(t, err)
	assert.Equal(t, "hello\n", string(content), "other files are kept")

	again, changed, err := repo.WriteBranch(t.Context(), "release", head, files, message)
	require.NoError(t, err)
	assert.False(t, changed, "the same changes on the same parent")
	assert.Equal(t, commit, again)

	files[changelog] = []byte("# Changelog\n\nMore.\n")
	rebuilt, changed, err := repo.WriteBranch(t.Context(), "release", head, files, message)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.NotEqual(t, commit, rebuilt)

	_, _, err = repo.WriteBranch(t.Context(), "main", head, files, message)
	require.Error(t, err, "the checked out branch")
	unchanged, err := repo.Head(t.Context())
	require.NoError(t, err)
	assert.Equal(t, head, unchanged)
}

// RemoteBuilder returns a repository with history, as Builder does, a Remote that pushes from it,
// and pushed, which returns the commit a ref points to on the remote, and false if it isn't there.
type RemoteBuilder func(t *testing.T, history ...Commit) (
	repo release.Repo, remote release.Remote, pushed func(ref string) (string, bool),
)

// RunRemoteContract tests that the remotes build makes push as release.Publish expects.
func RunRemoteContract(t *testing.T, build RemoteBuilder) {
	t.Helper()
	t.Run("PushTags", func(t *testing.T) {
		repo, remote, pushed := build(t, contractHistory...)
		log, err := repo.Log(t.Context(), "", "main")
		require.NoError(t, err)

		require.NoError(t, remote.PushTags(t.Context(), []string{firstTag}))
		commit, found := pushed("refs/tags/v1.0.0")
		assert.True(t, found)
		assert.Equal(t, log[0].SHA, commit, "the tag's commit")

		require.NoError(t, repo.CreateTag(t.Context(), "v2.0.0", log[1].SHA, "app 2.0.0"))
		require.NoError(t, remote.PushTags(t.Context(), []string{firstTag, "v2.0.0"}), "pushed tags are left alone")
		commit, found = pushed("refs/tags/v2.0.0")
		assert.True(t, found)
		assert.Equal(t, log[1].SHA, commit)

		require.NoError(t, remote.PushTags(t.Context(), nil), "no tags")
		require.Error(t, remote.PushTags(t.Context(), []string{"v3.0.0"}), "a tag that doesn't exist")
	})
	t.Run("PushBranch", func(t *testing.T) {
		repo, remote, pushed := build(t, contractHistory...)
		head, err := repo.Head(t.Context())
		require.NoError(t, err)
		files := map[string][]byte{changelog: []byte("# Changelog\n")}
		first, _, err := repo.WriteBranch(t.Context(), "release", head, files, "chore(release): one")
		require.NoError(t, err)

		changed, err := remote.PushBranch(t.Context(), "release", first)
		require.NoError(t, err)
		assert.True(t, changed)
		commit, found := pushed("refs/heads/release")
		assert.True(t, found)
		assert.Equal(t, first, commit)

		changed, err = remote.PushBranch(t.Context(), "release", first)
		require.NoError(t, err)
		assert.False(t, changed, "the remote already has it")

		files[changelog] = []byte("# Changelog\n\nMore.\n")
		rebuilt, _, err := repo.WriteBranch(t.Context(), "release", head, files, "chore(release): two")
		require.NoError(t, err)
		changed, err = remote.PushBranch(t.Context(), "release", rebuilt)
		require.NoError(t, err)
		assert.True(t, changed, "a rebuilt branch replaces the old one")
		commit, _ = pushed("refs/heads/release")
		assert.Equal(t, rebuilt, commit)
	})
}
