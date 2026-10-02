package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CallumKerson/release-bot/internal/release"
	"github.com/CallumKerson/release-bot/internal/release/releasetest"
	"github.com/CallumKerson/release-bot/internal/testing/gitrepo"
)

func open(t *testing.T, fixture *gitrepo.Repo) *Repo {
	t.Helper()
	repo, err := Open(t.Context(), fixture.Dir)
	require.NoError(t, err)
	return repo
}

// build makes a repository with history, as releasetest.Builder does.
func build(t *testing.T, history ...releasetest.Commit) *gitrepo.Repo {
	t.Helper()
	fixture := gitrepo.New(t)
	for _, commit := range history {
		for path, content := range commit.Files {
			fixture.Write(path, content)
		}
		fixture.Commit(commit.Message)
		for _, tag := range commit.Tags {
			fixture.Git("tag", "--annotate", "--message", tag, tag)
		}
	}
	return fixture
}

func TestContract(t *testing.T) {
	releasetest.RunContract(t, func(t *testing.T, history ...releasetest.Commit) release.Repo {
		t.Helper()
		return open(t, build(t, history...))
	})
}

func TestRemoteContract(t *testing.T) {
	releasetest.RunRemoteContract(t, func(t *testing.T, history ...releasetest.Commit) (
		release.Repo, release.Remote, func(string) (string, bool),
	) {
		t.Helper()
		fixture := build(t, history...)
		origin := withOrigin(t, fixture)
		repo := open(t, fixture)
		remote := repo.Remote("origin")
		return repo, remote, func(ref string) (string, bool) {
			out, err := exec.CommandContext(t.Context(), "git", "--git-dir", origin,
				"rev-parse", "--verify", "--quiet", ref+"^{commit}").Output()
			return strings.TrimSpace(string(out)), err == nil
		}
	})
}

// withOrigin gives fixture an empty bare repository as its origin, and returns the bare repository's path.
func withOrigin(t *testing.T, fixture *gitrepo.Repo) string {
	t.Helper()
	origin := filepath.Join(t.TempDir(), "origin.git")
	fixture.Git("init", "--quiet", "--bare", origin)
	fixture.Git("remote", "add", "origin", origin)
	return origin
}

func TestPushTagsRefusesAConflictingTag(t *testing.T) {
	fixture := build(t, releasetest.Commit{Message: "chore: one"}, releasetest.Commit{Message: "chore: two"})
	origin := withOrigin(t, fixture)
	fixture.Git("push", "--quiet", "origin", "HEAD~1:refs/tags/v1.0.0")
	fixture.Git("tag", "v1.0.0", "HEAD")

	err := open(t, fixture).Remote("origin").PushTags(t.Context(), []string{"v1.0.0"})
	require.ErrorIs(t, err, ErrTagConflict)
	assert.Equal(t, fixture.Git("rev-parse", "HEAD~1"), fixture.Git("--git-dir", origin, "rev-parse", "v1.0.0"))
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
	fixture.Commit("chore: other", "other.txt")
	third := fixture.Commit("chore: two", "m.json")
	repo := open(t, fixture)

	history, err := repo.FileHistory(t.Context(), "HEAD", "m.json")
	require.NoError(t, err)
	assert.Equal(t, []string{third, first}, history)
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
