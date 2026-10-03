package github

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CallumKerson/release-bot/internal/git"
	"github.com/CallumKerson/release-bot/internal/github/githubtest"
	"github.com/CallumKerson/release-bot/internal/release"
	"github.com/CallumKerson/release-bot/internal/release/releasetest"
	"github.com/CallumKerson/release-bot/internal/testing/gitrepo"
)

// onGitHub pushes a working repository's main branch and tags to a new fake GitHub,
// and returns the fake and a Host for it, releasing from branch.
func onGitHub(t *testing.T, work *gitrepo.Repo, branch string) (*githubtest.Server, *Host) {
	t.Helper()
	origin := filepath.Join(t.TempDir(), "origin.git")
	work.Git("init", "--quiet", "--bare", "--initial-branch", "main", origin)
	work.Git("remote", "add", "origin", origin)
	work.Git("push", "--quiet", "--tags", "origin", "main")
	server := githubtest.New(t, githubtest.Options{Repository: repository, Origin: origin})
	host, err := New(Options{Token: githubtest.Token, Repository: repository, APIURL: server.URL, Branch: branch})
	require.NoError(t, err)
	return server, host
}

func TestRepoContract(t *testing.T) {
	releasetest.RunContract(t, func(t *testing.T, history ...releasetest.Commit) release.Repo {
		t.Helper()
		_, host := onGitHub(t, releasetest.NewGit(t, history...), "")
		return host
	})
}

func TestWriteBranchSignsTheCommit(t *testing.T) {
	work := gitrepo.New(t)
	work.Commit("chore: init", "README.md")
	server, host := onGitHub(t, work, "")
	head, err := host.Head(t.Context())
	require.NoError(t, err)

	commit, changed, err := host.WriteBranch(t.Context(), "release", head,
		map[string][]byte{"CHANGELOG.md": []byte("# Changelog\n")}, "chore(release): app 1.0.0")
	require.NoError(t, err)
	assert.True(t, changed)
	assert.True(t, server.Verified(commit), "GitHub signs the release commit")
}

func TestTargetBranch(t *testing.T) {
	work := gitrepo.New(t)
	work.Commit("chore: init", "README.md")
	work.Git("branch", "stable")
	work.Commit("feat: next", "next.txt")
	server, host := onGitHub(t, work, "stable")
	work.Git("push", "--quiet", "origin", "stable")

	head, err := host.Head(t.Context())
	require.NoError(t, err)
	assert.Equal(t, work.Git("rev-parse", "stable"), head, "the target branch, not the default")

	_, _, err = host.WriteBranch(t.Context(), "stable", head, map[string][]byte{"a.txt": []byte("a\n")}, "chore: x")
	require.ErrorIs(t, err, ErrTargetBranch)
	_, _, err = host.WriteBranch(t.Context(), "main", head, map[string][]byte{"a.txt": []byte("a\n")}, "chore: x")
	require.NoError(t, err, "the default branch isn't protected when it isn't the target")

	_, _, err = host.WriteBranch(t.Context(), "release", head, map[string][]byte{"a.txt": []byte("a\n")}, "chore: x")
	require.NoError(t, err)
	_, err = host.EnsurePullRequest(t.Context(), &release.PullRequest{Branch: "release", Title: "chore: x"})
	require.NoError(t, err)
	assert.Equal(
		t,
		"stable",
		server.PullRequests()[0].GetBase().GetRef(),
		"release pull requests merge into the target",
	)
}

func TestLogAgreesWithGit(t *testing.T) {
	work := gitrepo.New(t)
	base := work.Commit("chore: init", "README.md")
	work.Git("switch", "--quiet", "--create", "topic")
	work.Commit("feat: on topic", "topic.txt")
	work.Git("mv", "README.md", "docs.md")
	work.Commit("docs: move the readme")
	work.Git("switch", "--quiet", "main")
	work.Commit("fix: on main", "main.txt")
	work.Git("merge", "--quiet", "--no-ff", "--message", "Merge topic", "topic")
	work.Commit("chore: after", "after.txt")
	_, host := onGitHub(t, work, "")
	repo, err := git.Open(t.Context(), work.Dir)
	require.NoError(t, err)
	head := work.Head()

	for _, since := range []string{"", base} {
		want, err := repo.Log(t.Context(), since, head)
		require.NoError(t, err)
		got, err := host.Log(t.Context(), since, head)
		require.NoError(t, err)
		assert.Equal(t, want, got, "since %q", since)
	}
}

func TestReadFileOverOneMegabyte(t *testing.T) {
	work := gitrepo.New(t)
	big := strings.Repeat("x", 1024*1024+1)
	work.Write("big.txt", big)
	work.Commit("chore: init")
	_, host := onGitHub(t, work, "")

	content, found, err := host.ReadFile(t.Context(), "main", "big.txt")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, big, string(content))
}
