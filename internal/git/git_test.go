package git

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CallumKerson/release-bot/internal/release"
	"github.com/CallumKerson/release-bot/internal/release/releasetest"
	"github.com/CallumKerson/release-bot/internal/testing/gitrepo"
	"github.com/CallumKerson/release-bot/internal/vcs"
)

func open(t *testing.T, fixture *gitrepo.Repo) *Repo {
	t.Helper()
	repo, err := Open(t.Context(), fixture.Dir)
	require.NoError(t, err)
	return repo
}

func TestContract(t *testing.T) {
	releasetest.RunContract(t, func(t *testing.T, history ...releasetest.Commit) release.Repo {
		t.Helper()
		return open(t, releasetest.NewGit(t, history...))
	})
}

func TestOpenFindsTheRoot(t *testing.T) {
	fixture := gitrepo.New(t)
	fixture.Commit("chore: init", "a/b/c.txt")
	repo, err := Open(t.Context(), filepath.Join(fixture.Dir, "a", "b"))
	require.NoError(t, err)
	want, err := filepath.EvalSymlinks(fixture.Dir)
	require.NoError(t, err)
	got, err := filepath.EvalSymlinks(repo.Root())
	require.NoError(t, err)
	assert.Equal(t, want, got)

	_, err = Open(t.Context(), t.TempDir())
	require.Error(t, err)
}

func TestHeadOfEmptyRepository(t *testing.T) {
	repo := open(t, gitrepo.New(t))
	_, err := repo.Head(t.Context())
	require.ErrorIs(t, err, ErrNoCommits)
}

func TestLog(t *testing.T) {
	fixture := gitrepo.New(t)
	first := fixture.Commit("chore: init", "README.md")
	second := fixture.Commit("feat(api): add thing\n\nWith a body.\n\nRefs: #1", "apps/api/a.go", "libs/x/x.go")
	fixture.Git("mv", "libs/x/x.go", "apps/api/x.go")
	third := fixture.Commit("refactor: move x")

	fixture.Git("switch", "--quiet", "-c", "topic", first)
	topic := fixture.Commit("fix: on a branch", "with space/file name.txt")
	fixture.Git("switch", "--quiet", "main")
	fixture.Git("merge", "--quiet", "--no-ff", "--no-edit", "topic")

	repo := open(t, fixture)
	head, err := repo.Head(t.Context())
	require.NoError(t, err)

	commits, err := repo.Log(t.Context(), "", head)
	require.NoError(t, err)
	shas := make([]string, 0, len(commits))
	for _, c := range commits {
		shas = append(shas, c.SHA)
	}
	assert.ElementsMatch(t, []string{topic, third, second, first}, shas, "merge commits are skipped")

	byID := map[string]int{}
	for i, c := range commits {
		byID[c.SHA] = i
	}
	assert.Equal(t, "feat(api): add thing\n\nWith a body.\n\nRefs: #1", commits[byID[second]].Message)
	assert.Equal(t, []string{"apps/api/a.go", "libs/x/x.go"}, commits[byID[second]].Files)
	assert.ElementsMatch(
		t,
		[]string{"apps/api/x.go", "libs/x/x.go"},
		commits[byID[third]].Files,
		"renames list both paths",
	)
	assert.Equal(t, []string{"with space/file name.txt"}, commits[byID[topic]].Files)

	since, err := repo.Log(t.Context(), second, head)
	require.NoError(t, err)
	assert.Len(t, since, 2)
}

func TestReadFile(t *testing.T) {
	fixture := gitrepo.New(t)
	first := fixture.Commit("chore: init", "a.txt")
	fixture.Write("a.txt", "changed in the working tree")
	repo := open(t, fixture)

	data, found, err := repo.ReadFile(t.Context(), first, "a.txt")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "a.txt\nchore: init\n", string(data))

	_, found, err = repo.ReadFile(t.Context(), first, "missing.txt")
	require.NoError(t, err)
	assert.False(t, found)

	_, found, err = repo.ReadFile(t.Context(), first+"^", "a.txt")
	require.NoError(t, err)
	assert.False(t, found, "the root commit has no parent")
}

func TestFileHistory(t *testing.T) {
	fixture := gitrepo.New(t)
	first := fixture.Commit("chore: one", "m.json")
	second := fixture.Commit("chore: other", "other.txt")
	third := fixture.Commit("chore: two", "m.json")
	repo := open(t, fixture)

	history, err := repo.FileHistory(t.Context(), "HEAD", "m.json")
	require.NoError(t, err)
	assert.Equal(t, []vcs.Commit{{SHA: third, Parents: []string{second}}, {SHA: first}}, history)
}

func TestTags(t *testing.T) {
	fixture := gitrepo.New(t)
	first := fixture.Commit("chore: init", "a.txt")
	fixture.Commit("chore: next", "b.txt")
	repo := open(t, fixture)

	_, found, err := repo.TagCommit(t.Context(), "v1.0.0")
	require.NoError(t, err)
	assert.False(t, found)

	require.NoError(t, repo.CreateTag(t.Context(), "v1.0.0", first, "v1.0.0"))
	sha, found, err := repo.TagCommit(t.Context(), "v1.0.0")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, first, sha, "annotated tags resolve to their commit")
	assert.Equal(t, "tag", fixture.Git("cat-file", "-t", "v1.0.0"))

	require.Error(t, repo.CreateTag(t.Context(), "v1.0.0", first, "again"))
}

func TestWriteBranch(t *testing.T) {
	fixture := gitrepo.New(t)
	head := fixture.Commit("chore: init", "keep.txt", "CHANGELOG.md")
	fixture.Write("dirty.txt", "uncommitted")
	repo := open(t, fixture)

	files := map[string][]byte{"CHANGELOG.md": []byte("# Changelog\n"), "new/dir/m.json": []byte("{}\n")}
	commit, changed, err := repo.WriteBranch(
		t.Context(),
		"release-bot/release",
		head,
		files,
		"chore(release): x\n\nbody",
	)
	require.NoError(t, err)
	assert.True(t, changed)

	assert.Equal(t, commit, fixture.Git("rev-parse", "release-bot/release"))
	assert.Equal(t, head, fixture.Git("rev-parse", "release-bot/release^"))
	assert.Equal(t, "chore(release): x\n\nbody", fixture.Git("log", "-1", "--format=%B", "release-bot/release"))
	assert.Equal(t, "# Changelog", fixture.Git("show", "release-bot/release:CHANGELOG.md"))
	assert.Equal(t, "{}", fixture.Git("show", "release-bot/release:new/dir/m.json"))
	assert.Equal(t, "keep.txt\nchore: init", fixture.Git("show", "release-bot/release:keep.txt"))

	assert.Equal(t, head, fixture.Head(), "HEAD doesn't move")
	assert.Equal(t, "?? dirty.txt", fixture.Status(), "the working tree and index are untouched")

	again, changed, err := repo.WriteBranch(
		t.Context(),
		"release-bot/release",
		head,
		files,
		"chore(release): x\n\nbody",
	)
	require.NoError(t, err)
	assert.False(t, changed)
	assert.Equal(t, commit, again)

	files["CHANGELOG.md"] = []byte("# Changelog\n\nmore\n")
	rebuilt, changed, err := repo.WriteBranch(t.Context(), "release-bot/release", head, files, "chore(release): y")
	require.NoError(t, err)
	assert.True(t, changed)
	assert.NotEqual(t, commit, rebuilt)
	assert.Equal(t, head, fixture.Git("rev-parse", "release-bot/release^"), "rebuilt from the parent, not stacked")
}

func TestWriteBranchRefusesTheCheckedOutBranch(t *testing.T) {
	fixture := gitrepo.New(t)
	head := fixture.Commit("chore: init", "a.txt")
	repo := open(t, fixture)
	_, _, err := repo.WriteBranch(t.Context(), "main", head, map[string][]byte{"a.txt": []byte("x")}, "chore: x")
	require.ErrorIs(t, err, ErrCheckedOut)
	assert.Equal(t, head, fixture.Head())
}

func TestFallbackIdentity(t *testing.T) {
	fixture := gitrepo.New(t)
	head := fixture.Commit("chore: init", "a.txt")
	empty := filepath.Join(t.TempDir(), "empty")
	require.NoError(t, os.WriteFile(empty, nil, 0o600))
	t.Setenv("GIT_CONFIG_GLOBAL", empty)
	repo := open(t, fixture)

	commit, _, err := repo.WriteBranch(
		t.Context(),
		"release",
		head,
		map[string][]byte{"b.txt": []byte("b")},
		"chore: x",
	)
	require.NoError(t, err)
	assert.Equal(t, "release-bot <release-bot@localhost>", fixture.Git("log", "-1", "--format=%an <%ae>", commit))

	require.NoError(t, repo.CreateTag(t.Context(), "v1", commit, "v1"))
	assert.Equal(
		t,
		"release-bot <release-bot@localhost>",
		fixture.Git("tag", "--list", "--format=%(taggername) %(taggeremail)", "v1"),
	)
}
