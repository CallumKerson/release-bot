package releasetest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CallumKerson/release-bot/internal/release"
)

// ErrNoBranch is returned when opening a pull request from a branch that doesn't exist.
var ErrNoBranch = errors.New("no such branch")

// PullRequest is a pull request on a FakeHost.
type PullRequest struct {
	Number int
	Branch string
	Title  string
	Body   string
	Open   bool
}

// Release is a release on a FakeHost.
type Release struct {
	Tag   string
	Notes string
}

// FakeHost is an in-memory release.Host for the repository a Fake holds.
type FakeHost struct {
	fake     *Fake
	pulls    []*PullRequest
	releases []Release
	writes   int
}

// NewFakeHost returns a host for the fake's repository.
func NewFakeHost(fake *Fake) *FakeHost {
	return &FakeHost{fake: fake}
}

// PullRequests returns every pull request, oldest first.
func (h *FakeHost) PullRequests() []PullRequest {
	out := make([]PullRequest, 0, len(h.pulls))
	for _, pull := range h.pulls {
		out = append(out, *pull)
	}
	return out
}

// Releases returns every release, oldest first.
func (h *FakeHost) Releases() []Release {
	return slices.Clone(h.releases)
}

// Writes counts the changes made to the host.
func (h *FakeHost) Writes() int {
	return h.writes
}

// Merge closes a pull request, as merging it would.
func (h *FakeHost) Merge(number int) {
	h.pulls[number-1].Open = false
}

// EnsurePullRequest opens a pull request from the branch, or updates the open one.
func (h *FakeHost) EnsurePullRequest(
	_ context.Context,
	request *release.PullRequest,
) (*release.PullRequestResult, error) {
	if _, ok := h.fake.Branch(request.Branch); !ok {
		return nil, fmt.Errorf("%w: %s", ErrNoBranch, request.Branch)
	}
	for _, pull := range h.pulls {
		if !pull.Open || pull.Branch != request.Branch {
			continue
		}
		result := &release.PullRequestResult{Number: pull.Number, URL: pullURL(pull.Number)}
		if pull.Title == request.Title && pull.Body == request.Body {
			result.Action = release.PullRequestUnchanged
			return result, nil
		}
		pull.Title, pull.Body = request.Title, request.Body
		h.writes++
		result.Action = release.PullRequestUpdated
		return result, nil
	}
	pull := &PullRequest{
		Number: len(h.pulls) + 1,
		Branch: request.Branch,
		Title:  request.Title,
		Body:   request.Body,
		Open:   true,
	}
	h.pulls = append(h.pulls, pull)
	h.writes++
	return &release.PullRequestResult{
		Number: pull.Number, URL: pullURL(pull.Number), Action: release.PullRequestOpened,
	}, nil
}

// EnsureRelease publishes a release of the tag unless it has one. As on GitHub, a missing tag is created.
func (h *FakeHost) EnsureRelease(_ context.Context, tag *release.Tag) (url string, created bool, err error) {
	url = "https://example.test/releases/" + tag.Name
	if slices.ContainsFunc(h.releases, func(r Release) bool { return r.Tag == tag.Name }) {
		return url, false, nil
	}
	if _, ok := h.fake.tags[tag.Name]; !ok {
		if _, ok := h.fake.commits[tag.ReleaseCommit]; !ok {
			return "", false, fmt.Errorf("%w: %s", ErrUnknownRevision, tag.ReleaseCommit)
		}
		h.fake.tags[tag.Name] = tag.ReleaseCommit
	}
	h.releases = append(h.releases, Release{Tag: tag.Name, Notes: tag.Notes})
	h.writes++
	return url, true, nil
}

func pullURL(number int) string {
	return fmt.Sprintf("https://example.test/pull/%d", number)
}

// HostFixture is a host, and the repository on it.
type HostFixture struct {
	Repo release.Repo
	Host release.Host
	// Merge merges a pull request on the host.
	Merge func(t *testing.T, number int)
}

// HostBuilder returns a HostFixture whose repository's checked out branch, main, has history, oldest commit first.
type HostBuilder func(t *testing.T, history ...Commit) *HostFixture

// RunHostContract tests that the hosts build makes behave as release.Publish expects.
func RunHostContract(t *testing.T, build HostBuilder) {
	t.Helper()
	t.Run("EnsurePullRequest", func(t *testing.T) { testEnsurePullRequest(t, build(t, contractHistory...)) })
	t.Run("EnsureRelease", func(t *testing.T) { testEnsureRelease(t, build(t, contractHistory...)) })
}

func testEnsurePullRequest(t *testing.T, fixture *HostFixture) {
	t.Helper()
	ctx := t.Context()
	head, err := fixture.Repo.Head(ctx)
	require.NoError(t, err)

	request := &release.PullRequest{Branch: "release", Title: "chore(release): app 1.1.0", Body: "## app 1.1.0"}
	_, err = fixture.Host.EnsurePullRequest(ctx, request)
	require.Error(t, err, "a branch that doesn't exist")

	_, _, err = fixture.Repo.WriteBranch(ctx, "release", head,
		map[string][]byte{changelog: []byte("# Changelog\n")}, "chore(release): app 1.1.0")
	require.NoError(t, err)
	opened, err := fixture.Host.EnsurePullRequest(ctx, request)
	require.NoError(t, err)
	assert.Equal(t, release.PullRequestOpened, opened.Action)
	assert.NotEmpty(t, opened.URL)

	again, err := fixture.Host.EnsurePullRequest(ctx, request)
	require.NoError(t, err)
	assert.Equal(t, &release.PullRequestResult{
		Number: opened.Number, URL: opened.URL,
		Action: release.PullRequestUnchanged,
	}, again)

	request.Body = "## app 1.2.0"
	updated, err := fixture.Host.EnsurePullRequest(ctx, request)
	require.NoError(t, err)
	assert.Equal(t, release.PullRequestUpdated, updated.Action)
	assert.Equal(t, opened.Number, updated.Number, "the same pull request")

	fixture.Merge(t, opened.Number)
	next, err := fixture.Host.EnsurePullRequest(ctx, request)
	require.NoError(t, err)
	assert.Equal(t, release.PullRequestOpened, next.Action, "a merged pull request isn't reused")
	assert.NotEqual(t, opened.Number, next.Number)
}

func testEnsureRelease(t *testing.T, fixture *HostFixture) {
	t.Helper()
	ctx := t.Context()
	log, err := fixture.Repo.Log(ctx, "", "main")
	require.NoError(t, err)

	tag := &release.Tag{Package: appName, Version: "1.0.0", Name: firstTag, ReleaseCommit: log[0].SHA, Notes: "Notes"}
	url, created, err := fixture.Host.EnsureRelease(ctx, tag)
	require.NoError(t, err)
	assert.True(t, created)
	assert.NotEmpty(t, url)

	again, created, err := fixture.Host.EnsureRelease(ctx, tag)
	require.NoError(t, err)
	assert.False(t, created, "the tag already has a release")
	assert.Equal(t, url, again)

	unpushed := &release.Tag{Package: appName, Version: "2.0.0", Name: secondTag, ReleaseCommit: log[1].SHA}
	_, created, err = fixture.Host.EnsureRelease(ctx, unpushed)
	require.NoError(t, err)
	assert.True(t, created)
	commit, found, err := fixture.Repo.TagCommit(ctx, secondTag)
	require.NoError(t, err)
	assert.True(t, found, "releasing a tag that doesn't exist creates it")
	assert.Equal(t, log[1].SHA, commit, "on the release commit")
}
