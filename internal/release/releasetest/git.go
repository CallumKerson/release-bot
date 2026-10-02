package releasetest

import (
	"testing"

	"github.com/CallumKerson/release-bot/internal/testing/gitrepo"
)

// NewGit builds a git repository whose main branch has history, oldest commit first, as NewFake does.
func NewGit(t *testing.T, history ...Commit) *gitrepo.Repo {
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
