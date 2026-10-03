package githubtest_test

import (
	"fmt"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/google/go-github/v92/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CallumKerson/release-bot/internal/github/githubtest"
	"github.com/CallumKerson/release-bot/internal/testing/gitrepo"
)

const (
	owner = "octo-org"
	name  = "widgets"
)

// fixture is a working repository whose origin is the fake's bare repository.
type fixture struct {
	work   *gitrepo.Repo
	origin string
	server *githubtest.Server
	client *github.Client
}

func newFixture(t *testing.T, opts githubtest.Options) *fixture {
	t.Helper()
	work := gitrepo.New(t)
	work.Commit("chore: init", "README.md")
	origin := filepath.Join(t.TempDir(), "origin.git")
	work.Git("clone", "--quiet", "--bare", work.Dir, origin)
	work.Git("remote", "add", "origin", origin)

	opts.Repository, opts.Origin = owner+"/"+name, origin
	server := githubtest.New(t, opts)
	return &fixture{work: work, origin: origin, server: server, client: newClient(t, server, githubtest.Token)}
}

func newClient(t *testing.T, server *githubtest.Server, token string) *github.Client {
	t.Helper()
	client, err := github.NewClient(github.WithAuthToken(token), github.WithURLs(&server.URL, &server.URL))
	require.NoError(t, err)
	return client
}

// pushBranch pushes a new branch with one commit on top of main.
func (hub *fixture) pushBranch(t *testing.T, branch string) {
	t.Helper()
	hub.work.Git("switch", "--quiet", "--create", branch, "main")
	hub.work.Commit("feat: "+branch+"\n\nBody of "+branch+".", branch+".txt")
	hub.work.Git("push", "--quiet", "origin", branch)
	hub.work.Git("switch", "--quiet", "main")
}

func (hub *fixture) originRev(t *testing.T, rev string) string {
	t.Helper()
	return hub.work.Git("--git-dir", hub.origin, "rev-parse", rev)
}

func (hub *fixture) createPull(t *testing.T, branch string) *github.PullRequest {
	t.Helper()
	pull, _, err := hub.client.PullRequests.Create(t.Context(), owner, name, github.CreatePullRequest{
		Title: new("Add " + branch), Head: branch, Base: "main", Body: new("About " + branch),
	})
	require.NoError(t, err)
	return pull
}

// validation returns the validation errors of a 422 response.
func validation(t *testing.T, err error) []github.Error {
	t.Helper()
	var response *github.ErrorResponse
	require.ErrorAs(t, err, &response)
	require.Equal(t, http.StatusUnprocessableEntity, response.Response.StatusCode, response.Message)
	return response.Errors
}

func status(t *testing.T, err error) int {
	t.Helper()
	var response *github.ErrorResponse
	require.ErrorAs(t, err, &response)
	return response.Response.StatusCode
}

func TestRequiresTheToken(t *testing.T) {
	hub := newFixture(t, githubtest.Options{})
	_, _, err := newClient(t, hub.server, "wrong").Repositories.Get(t.Context(), owner, name)
	assert.Equal(t, http.StatusUnauthorized, status(t, err))
}

func TestGetRepository(t *testing.T) {
	hub := newFixture(t, githubtest.Options{})
	repo, _, err := hub.client.Repositories.Get(t.Context(), owner, name)
	require.NoError(t, err)
	assert.Equal(t, "main", repo.GetDefaultBranch())
	assert.Equal(t, owner+"/"+name, repo.GetFullName())

	_, _, err = hub.client.Repositories.Get(t.Context(), owner, "other")
	assert.Equal(t, http.StatusNotFound, status(t, err))
}

func TestCreatePullRequest(t *testing.T) {
	hub := newFixture(t, githubtest.Options{})
	hub.pushBranch(t, "feature")

	pull := hub.createPull(t, "feature")
	assert.Equal(t, 1, pull.GetNumber())
	assert.Equal(t, "open", pull.GetState())
	assert.Equal(t, "https://github.com/octo-org/widgets/pull/1", pull.GetHTMLURL())
	assert.Equal(t, owner+":feature", pull.GetHead().GetLabel())
	assert.Equal(t, hub.originRev(t, "feature"), pull.GetHead().GetSHA())

	t.Run("again for the same branch", func(t *testing.T) {
		_, _, err := hub.client.PullRequests.Create(t.Context(), owner, name,
			github.CreatePullRequest{Title: new("Again"), Head: owner + ":feature", Base: "main"})
		errs := validation(t, err)
		require.Len(t, errs, 1)
		assert.Equal(t, "A pull request already exists for octo-org:feature.", errs[0].Message)
	})
	t.Run("for a branch that isn't pushed", func(t *testing.T) {
		_, _, err := hub.client.PullRequests.Create(t.Context(), owner, name,
			github.CreatePullRequest{Title: new("Missing"), Head: "missing", Base: "main"})
		assert.Equal(t, []github.Error{{Resource: "PullRequest", Field: "head", Code: "invalid"}}, validation(t, err))
	})
	t.Run("for a branch with nothing new", func(t *testing.T) {
		hub.work.Git("push", "--quiet", "origin", "main:refs/heads/same")
		_, _, err := hub.client.PullRequests.Create(t.Context(), owner, name,
			github.CreatePullRequest{Title: new("Same"), Head: "same", Base: "main"})
		errs := validation(t, err)
		require.Len(t, errs, 1)
		assert.Equal(t, "No commits between main and same", errs[0].Message)
	})
}

func TestPullRequestsFollowTheirBranch(t *testing.T) {
	hub := newFixture(t, githubtest.Options{})
	hub.pushBranch(t, "feature")
	hub.createPull(t, "feature")

	hub.work.Git("switch", "--quiet", "feature")
	hub.work.Commit("fix: more", "more.txt")
	hub.work.Git("push", "--quiet", "origin", "feature")

	pull, _, err := hub.client.PullRequests.List(t.Context(), owner, name, &github.PullRequestListOptions{
		Head: owner + ":feature",
	})
	require.NoError(t, err)
	require.Len(t, pull, 1)
	assert.Equal(t, hub.originRev(t, "feature"), pull[0].GetHead().GetSHA(), "a push moves the pull request's head")
}

func TestListPullRequestsFiltersAndPaginates(t *testing.T) {
	hub := newFixture(t, githubtest.Options{MaxPageSize: 2})
	for _, branch := range []string{"one", "two", "three"} {
		hub.pushBranch(t, branch)
		hub.createPull(t, branch)
	}

	var titles []string
	opts := &github.PullRequestListOptions{State: "open"}
	for {
		page, resp, err := hub.client.PullRequests.List(t.Context(), owner, name, opts)
		require.NoError(t, err)
		assert.LessOrEqual(t, len(page), 2)
		for _, pull := range page {
			titles = append(titles, pull.GetTitle())
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	assert.Equal(t, []string{"Add three", "Add two", "Add one"}, titles, "newest first, across pages")

	filtered, _, err := hub.client.PullRequests.List(t.Context(), owner, name, &github.PullRequestListOptions{
		Head: owner + ":two", Base: "main",
	})
	require.NoError(t, err)
	require.Len(t, filtered, 1)
	assert.Equal(t, "Add two", filtered[0].GetTitle())

	closed, _, err := hub.client.PullRequests.List(t.Context(), owner, name, &github.PullRequestListOptions{
		State: "closed",
	})
	require.NoError(t, err)
	assert.Empty(t, closed)
}

func TestEditPullRequest(t *testing.T) {
	hub := newFixture(t, githubtest.Options{})
	hub.pushBranch(t, "feature")
	hub.createPull(t, "feature")

	pull, _, err := hub.client.PullRequests.Edit(t.Context(), owner, name, 1, &github.PullRequest{
		Title: new("New title"), Body: new("New body"),
	})
	require.NoError(t, err)
	assert.Equal(t, "New title", pull.GetTitle())
	assert.Equal(t, "New body", hub.server.PullRequests()[0].GetBody())

	_, _, err = hub.client.PullRequests.Edit(t.Context(), owner, name, 9, &github.PullRequest{Title: new("x")})
	assert.Equal(t, http.StatusNotFound, status(t, err))
}

func TestMergePullRequest(t *testing.T) {
	tests := map[string]struct {
		method  string
		message string
	}{
		"merge commit": {
			method:  githubtest.MergeCommit,
			message: "Merge pull request #1 from octo-org/feature\n\nAdd feature",
		},
		"squash": {
			method:  githubtest.Squash,
			message: "Add feature (#1)\n\nBody of feature.",
		},
		"rebase": {
			method:  githubtest.Rebase,
			message: "feat: feature\n\nBody of feature.",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			hub := newFixture(t, githubtest.Options{})
			hub.pushBranch(t, "feature")
			hub.createPull(t, "feature")
			head := hub.originRev(t, "feature")

			merged, err := hub.server.MergePullRequest(t.Context(), 1, test.method)
			require.NoError(t, err)
			assert.Equal(t, hub.originRev(t, "main"), merged)
			assert.Equal(t, test.message, hub.work.Git("--git-dir", hub.origin, "log", "-1", "--format=%B", "main"))
			assert.NotEqual(t, head, merged, "GitHub always makes a new commit")

			pull := hub.server.PullRequests()[0]
			assert.True(t, pull.GetMerged())
			assert.Equal(t, "closed", pull.GetState())
			assert.Equal(t, merged, pull.GetMergeCommitSHA())
			assert.Equal(t, head, pull.GetHead().GetSHA(), "a merged pull request keeps the head it merged")

			_, err = hub.server.MergePullRequest(t.Context(), 1, test.method)
			require.ErrorIs(t, err, githubtest.ErrNotMergeable, "a merged pull request")
		})
	}
}

func TestReleases(t *testing.T) {
	hub := newFixture(t, githubtest.Options{})
	hub.work.Git("tag", "--annotate", "--message", "v1", "v1.0.0")
	hub.work.Git("push", "--quiet", "origin", "v1.0.0")

	_, _, err := hub.client.Repositories.GetReleaseByTag(t.Context(), owner, name, "v1.0.0")
	assert.Equal(t, http.StatusNotFound, status(t, err))

	created, _, err := hub.client.Repositories.CreateRelease(t.Context(), owner, name, github.CreateReleaseRequest{
		TagName: "v1.0.0", Name: new("v1.0.0"), Body: new("Notes"),
	})
	require.NoError(t, err)
	assert.Equal(t, "https://github.com/octo-org/widgets/releases/tag/v1.0.0", created.HTMLURL)

	got, _, err := hub.client.Repositories.GetReleaseByTag(t.Context(), owner, name, "v1.0.0")
	require.NoError(t, err)
	assert.Equal(t, "Notes", got.GetBody())

	_, _, err = hub.client.Repositories.CreateRelease(t.Context(), owner, name, github.CreateReleaseRequest{
		TagName: "v1.0.0",
	})
	assert.Equal(
		t,
		[]github.Error{{Resource: "Release", Field: "tag_name", Code: "already_exists"}},
		validation(t, err),
	)
}

func TestReleasingATagThatIsNotPushedCreatesIt(t *testing.T) {
	hub := newFixture(t, githubtest.Options{})
	first := hub.work.Head()
	hub.work.Commit("feat: second", "second.txt")
	hub.work.Git("push", "--quiet", "origin", "main")

	_, _, err := hub.client.Repositories.CreateRelease(t.Context(), owner, name, github.CreateReleaseRequest{
		TagName: "v1.0.0", TargetCommitish: new(first),
	})
	require.NoError(t, err)
	assert.Equal(t, first, hub.originRev(t, "v1.0.0"), "a lightweight tag on the target")

	_, _, err = hub.client.Repositories.CreateRelease(t.Context(), owner, name, github.CreateReleaseRequest{
		TagName: "v2.0.0",
	})
	require.NoError(t, err)
	assert.Equal(t, hub.originRev(t, "main"), hub.originRev(t, "v2.0.0"), "without a target, the default branch")
}

func TestRecordsRequests(t *testing.T) {
	hub := newFixture(t, githubtest.Options{})
	_, _, err := hub.client.Repositories.Get(t.Context(), owner, name)
	require.NoError(t, err)
	_, _, err = hub.client.Repositories.CreateRelease(
		t.Context(),
		owner,
		name,
		github.CreateReleaseRequest{TagName: "v1"},
	)
	require.NoError(t, err)

	assert.Equal(t, []githubtest.Request{
		{Method: http.MethodGet, Path: "/repos/octo-org/widgets"},
		{Method: http.MethodPost, Path: "/repos/octo-org/widgets/releases"},
	}, hub.server.Requests())
	assert.Equal(t, []githubtest.Request{{Method: http.MethodPost, Path: "/repos/octo-org/widgets/releases"}},
		hub.server.Writes())
}

// strict records the errors a fake reports when the test ends, instead of failing the test.
type strict struct {
	testing.TB

	cleanups []func()
	errors   []string
}

func (s *strict) Cleanup(cleanup func()) { s.cleanups = append(s.cleanups, cleanup) }

func (s *strict) Errorf(format string, args ...any) {
	s.errors = append(s.errors, fmt.Sprintf(format, args...))
}

func TestFailsTheTestOnRequestsItDoesNotServe(t *testing.T) {
	recorder := &strict{TB: t}
	server := githubtest.New(recorder, githubtest.Options{Repository: owner + "/" + name, Origin: t.TempDir()})
	client := newClient(t, server, githubtest.Token)

	_, _, err := client.Issues.Get(t.Context(), owner, name, 1)
	assert.Equal(t, http.StatusNotImplemented, status(t, err))
	assert.Equal(t, []string{"GET /repos/octo-org/widgets/issues/1"}, server.Unexpected())

	for _, cleanup := range recorder.cleanups {
		cleanup()
	}
	assert.Equal(t, []string{"githubtest: unexpected request GET /repos/octo-org/widgets/issues/1"}, recorder.errors)
}
