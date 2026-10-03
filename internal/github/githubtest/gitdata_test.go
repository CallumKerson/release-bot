package githubtest_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-github/v92/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CallumKerson/release-bot/internal/github/githubtest"
)

func TestRefs(t *testing.T) {
	hub := newFixture(t, githubtest.Options{})
	main := hub.originRev(t, "main")
	hub.work.Git("tag", "--annotate", "--message", "v1", "v1.0.0")
	hub.work.Git("push", "--quiet", "origin", "v1.0.0")

	ref, _, err := hub.client.Git.GetRef(t.Context(), owner, name, "heads/main")
	require.NoError(t, err)
	assert.Equal(t, "refs/heads/main", ref.GetRef())
	assert.Equal(t, "commit", ref.GetObject().GetType())
	assert.Equal(t, main, ref.GetObject().GetSHA())

	ref, _, err = hub.client.Git.GetRef(t.Context(), owner, name, "tags/v1.0.0")
	require.NoError(t, err)
	assert.Equal(t, "tag", ref.GetObject().GetType(), "an annotated tag points at its tag object")
	assert.Equal(t, hub.originRev(t, "refs/tags/v1.0.0"), ref.GetObject().GetSHA())

	_, _, err = hub.client.Git.GetRef(t.Context(), owner, name, "heads/missing")
	assert.Equal(t, http.StatusNotFound, status(t, err))

	created, _, err := hub.client.Git.CreateRef(t.Context(), owner, name, github.CreateRef{
		Ref: "refs/heads/topic", SHA: main,
	})
	require.NoError(t, err)
	assert.Equal(t, "refs/heads/topic", created.GetRef())
	assert.Equal(t, main, hub.originRev(t, "topic"))

	_, _, err = hub.client.Git.CreateRef(t.Context(), owner, name, github.CreateRef{Ref: "refs/heads/topic", SHA: main})
	assert.Equal(t, http.StatusUnprocessableEntity, status(t, err), "a ref that already exists")
	_, _, err = hub.client.Git.CreateRef(t.Context(), owner, name, github.CreateRef{
		Ref: "refs/heads/other", SHA: strings.Repeat("0", 40),
	})
	assert.Equal(t, http.StatusUnprocessableEntity, status(t, err), "an object that doesn't exist")
}

func TestUpdateRefOnlyFastForwardsWithoutForce(t *testing.T) {
	hub := newFixture(t, githubtest.Options{})
	first := hub.originRev(t, "main")
	hub.pushBranch(t, "topic")
	topic := hub.originRev(t, "topic")

	_, _, err := hub.client.Git.UpdateRef(t.Context(), owner, name, "heads/topic", github.UpdateRef{SHA: first})
	assert.Equal(t, http.StatusUnprocessableEntity, status(t, err), "going back isn't a fast-forward")
	assert.Equal(t, topic, hub.originRev(t, "topic"))

	_, _, err = hub.client.Git.UpdateRef(t.Context(), owner, name, "heads/topic", github.UpdateRef{
		SHA: first, Force: new(true),
	})
	require.NoError(t, err)
	assert.Equal(t, first, hub.originRev(t, "topic"))

	_, _, err = hub.client.Git.UpdateRef(t.Context(), owner, name, "heads/topic", github.UpdateRef{SHA: topic})
	require.NoError(t, err, "a fast-forward")
	assert.Equal(t, topic, hub.originRev(t, "topic"))

	_, _, err = hub.client.Git.UpdateRef(t.Context(), owner, name, "heads/missing", github.UpdateRef{SHA: first})
	assert.Equal(t, http.StatusUnprocessableEntity, status(t, err), "a ref that doesn't exist")
}

func TestTagObjects(t *testing.T) {
	hub := newFixture(t, githubtest.Options{})
	main := hub.originRev(t, "main")

	created, _, err := hub.client.Git.CreateTag(t.Context(), owner, name, github.CreateTag{
		Tag: "v1.0.0", Message: "app 1.0.0", Object: main, Type: "commit",
	})
	require.NoError(t, err)
	assert.Equal(t, "v1.0.0", created.GetTag())
	assert.Equal(t, main, created.GetObject().GetSHA())
	assert.Equal(t, "github-actions[bot]", created.GetTagger().GetName(), "the token's bot, without a tagger")

	got, _, err := hub.client.Git.GetTag(t.Context(), owner, name, created.GetSHA())
	require.NoError(t, err)
	assert.Equal(t, "app 1.0.0\n", got.GetMessage())
	assert.Equal(t, "commit", got.GetObject().GetType())

	assert.Empty(
		t,
		hub.work.Git("--git-dir", hub.origin, "tag", "--list"),
		"creating a tag object doesn't create its ref",
	)

	_, _, err = hub.client.Git.CreateTag(t.Context(), owner, name, github.CreateTag{
		Tag: "v2.0.0", Message: "app 2.0.0", Object: main, Type: "tree",
	})
	assert.Equal(t, http.StatusUnprocessableEntity, status(t, err), "the wrong type of object")

	_, _, err = hub.client.Git.GetTag(t.Context(), owner, name, main)
	assert.Equal(t, http.StatusNotFound, status(t, err), "a commit isn't a tag")
}

func TestCreateCommit(t *testing.T) {
	hub := newFixture(t, githubtest.Options{})
	main := hub.originRev(t, "main")
	parent, _, err := hub.client.Git.GetCommit(t.Context(), owner, name, main)
	require.NoError(t, err)
	assert.Equal(t, "chore: init", parent.GetMessage())
	assert.False(t, parent.GetVerification().GetVerified(), "a pushed commit isn't signed")

	tree, _, err := hub.client.Git.CreateTree(t.Context(), owner, name, parent.GetTree().GetSHA(), []*github.TreeEntry{
		{Path: new("docs/CHANGELOG.md"), Mode: new("100644"), Type: new("blob"), Content: new("# Changelog\n")},
	})
	require.NoError(t, err)

	commit, _, err := hub.client.Git.CreateCommit(t.Context(), owner, name, github.Commit{
		Message: new("chore(release): app 1.1.0"), Tree: tree, Parents: []*github.Commit{{SHA: new(main)}},
	}, nil)
	require.NoError(t, err)
	assert.True(t, commit.GetVerification().GetVerified(), "GitHub signs a bot's commit")
	assert.True(t, hub.server.Verified(commit.GetSHA()))
	assert.Equal(t, "github-actions[bot]", commit.GetAuthor().GetName())
	assert.Equal(t, "GitHub", commit.GetCommitter().GetName())
	assert.Equal(t, []string{main}, []string{commit.Parents[0].GetSHA()})
	assert.Equal(t, "# Changelog", hub.work.Git("--git-dir", hub.origin, "show", commit.GetSHA()+":docs/CHANGELOG.md"))
	assert.Equal(
		t,
		"README.md\nchore: init",
		hub.work.Git("--git-dir", hub.origin, "show", commit.GetSHA()+":README.md"),
		"the base tree's files are kept",
	)

	authored, _, err := hub.client.Git.CreateCommit(t.Context(), owner, name, github.Commit{
		Message: new("chore: by someone"), Tree: tree, Parents: []*github.Commit{{SHA: new(main)}},
		Author: &github.CommitAuthor{Name: new("Someone"), Email: new("someone@example.com")},
	}, nil)
	require.NoError(t, err)
	assert.False(t, authored.GetVerification().GetVerified(), "a commit with an author isn't signed")
	assert.Equal(t, "Someone", authored.GetCommitter().GetName(), "the committer defaults to the author")

	_, _, err = hub.client.Git.CreateCommit(t.Context(), owner, name, github.Commit{
		Message: new("chore: broken"), Tree: &github.Tree{SHA: new(main)},
	}, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, status(t, err), "a commit isn't a tree")
}

func TestCreateTreeDeletes(t *testing.T) {
	hub := newFixture(t, githubtest.Options{})
	parent, _, err := hub.client.Git.GetCommit(t.Context(), owner, name, hub.originRev(t, "main"))
	require.NoError(t, err)

	tree, _, err := hub.client.Git.CreateTree(t.Context(), owner, name, parent.GetTree().GetSHA(), []*github.TreeEntry{
		{Path: new("README.md"), Mode: new("100644"), Type: new("blob")},
	})
	require.NoError(t, err)
	assert.Empty(t, hub.work.Git("--git-dir", hub.origin, "ls-tree", tree.GetSHA()))
}

func TestContents(t *testing.T) {
	hub := newFixture(t, githubtest.Options{})
	first := hub.originRev(t, "main")
	hub.work.Write("big.txt", strings.Repeat("x", 1024*1024+1))
	hub.work.Commit("chore: more", "README.md")
	hub.work.Git("push", "--quiet", "origin", "main")

	file, _, _, err := hub.client.Repositories.GetContents(t.Context(), owner, name, "README.md",
		&github.RepositoryContentGetOptions{Ref: first})
	require.NoError(t, err)
	content, err := file.GetContent()
	require.NoError(t, err)
	assert.Equal(t, "README.md\nchore: init\n", content)

	file, _, _, err = hub.client.Repositories.GetContents(t.Context(), owner, name, "README.md", nil)
	require.NoError(t, err)
	content, err = file.GetContent()
	require.NoError(t, err)
	assert.Equal(t, "README.md\nchore: more\n", content, "without a ref, the default branch")

	assert.Equal(t, http.StatusNotFound, hub.contentsStatus(t, "missing.txt", ""), "a missing file")
	assert.Equal(t, http.StatusNotFound, hub.contentsStatus(t, "README.md", "missing"), "a missing ref")

	big, _, _, err := hub.client.Repositories.GetContents(t.Context(), owner, name, "big.txt", nil)
	require.NoError(t, err)
	assert.Equal(t, "none", big.GetEncoding(), "files over 1 MB have no content")
	raw, _, err := hub.client.Git.GetBlobRaw(t.Context(), owner, name, big.GetSHA())
	require.NoError(t, err)
	assert.Len(t, raw, 1024*1024+1)
}

// contentsStatus returns the status of a failed request for a file's contents at ref.
func (hub *fixture) contentsStatus(t *testing.T, path, ref string) int {
	t.Helper()
	file, _, _, err := hub.client.Repositories.GetContents(t.Context(), owner, name, path,
		&github.RepositoryContentGetOptions{Ref: ref})
	require.Nil(t, file)
	return status(t, err)
}

func TestListCommits(t *testing.T) {
	hub := newFixture(t, githubtest.Options{MaxPageSize: 2})
	first := hub.originRev(t, "main")
	second := hub.work.Commit("feat: two", "m.json")
	third := hub.work.Commit("feat: three", "other.txt")
	fourth := hub.work.Commit("feat: four", "m.json")
	hub.work.Git("push", "--quiet", "origin", "main")

	var shas []string
	opts := &github.CommitsListOptions{SHA: "main"}
	for {
		commits, resp, err := hub.client.Repositories.ListCommits(t.Context(), owner, name, opts)
		require.NoError(t, err)
		for _, commit := range commits {
			shas = append(shas, commit.GetSHA())
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	assert.Equal(t, []string{fourth, third, second, first}, shas, "newest first, across pages")

	commits, _, err := hub.client.Repositories.ListCommits(t.Context(), owner, name,
		&github.CommitsListOptions{SHA: fourth, Path: "m.json"})
	require.NoError(t, err)
	require.Len(t, commits, 2, "the commits that changed the path")
	assert.Equal(t, fourth, commits[0].GetSHA())
	assert.Equal(t, third, commits[0].Parents[0].GetSHA())
	assert.Equal(t, "feat: four", commits[0].GetCommit().GetMessage())
}

func TestGetCommitListsFiles(t *testing.T) {
	hub := newFixture(t, githubtest.Options{})
	first := hub.originRev(t, "main")
	hub.work.Git("mv", "README.md", "docs.md")
	hub.work.Commit("docs: move", "added.txt")
	hub.work.Git("push", "--quiet", "origin", "main")

	commit, _, err := hub.client.Repositories.GetCommit(t.Context(), owner, name, "main", nil)
	require.NoError(t, err)
	assert.Equal(t, first, commit.Parents[0].GetSHA())
	files := map[string]string{}
	for _, file := range commit.Files {
		files[file.GetFilename()] = file.GetStatus() + " " + file.GetPreviousFilename()
	}
	assert.Equal(t, map[string]string{"added.txt": "added ", "docs.md": "renamed README.md"}, files)

	root, _, err := hub.client.Repositories.GetCommit(t.Context(), owner, name, first, nil)
	require.NoError(t, err)
	require.Len(t, root.Files, 1, "the root commit adds its files")
	assert.Equal(t, "added", root.Files[0].GetStatus())
}

func TestCompareCommits(t *testing.T) {
	hub := newFixture(t, githubtest.Options{})
	base := hub.originRev(t, "main")
	second := hub.work.Commit("feat: two", "two.txt")
	third := hub.work.Commit("feat: three", "three.txt")
	hub.work.Git("push", "--quiet", "origin", "main")

	comparison, _, err := hub.client.Repositories.CompareCommits(t.Context(), owner, name, base, "main", nil)
	require.NoError(t, err)
	assert.Equal(t, "ahead", comparison.GetStatus())
	assert.Equal(t, 2, comparison.GetAheadBy())
	require.Len(t, comparison.Commits, 2)
	assert.Equal(t, second, comparison.Commits[0].GetSHA(), "oldest first")
	assert.Equal(t, third, comparison.Commits[1].GetSHA())
	assert.Len(t, comparison.Files, 2)

	_, _, err = hub.client.Repositories.CompareCommits(t.Context(), owner, name, "missing", "main", nil)
	assert.Equal(t, http.StatusNotFound, status(t, err))
}
