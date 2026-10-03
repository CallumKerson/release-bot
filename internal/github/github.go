// Package github releases on GitHub through its REST API: it reads and writes the repository's git data,
// opens release pull requests, and publishes releases. Nothing needs a checkout.
package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	gogithub "github.com/google/go-github/v92/github"

	"github.com/CallumKerson/release-bot/internal/release"
)

const (
	// maxBody is the most characters GitHub allows in a pull request or release body.
	maxBody   = 65536
	truncated = "\n\n*Truncated: see the changelog for the full notes.*"
)

// ErrSettings is returned for settings that can't reach a GitHub repository.
var ErrSettings = errors.New("invalid GitHub settings")

// Options say which repository to release on, and how to reach it.
type Options struct {
	Token string
	// Repository is the repository's "owner/name".
	Repository string
	// APIURL is the base URL of the REST API. Empty means api.github.com.
	APIURL string
	// Branch is the branch releases are made from, and release pull requests merge into.
	// Empty means the repository's default branch.
	Branch string
}

// Host is a GitHub repository.
type Host struct {
	client      *gogithub.Client
	owner, name string
	// target is the branch releases are made from, and release pull requests merge into, once it is known.
	target string
	// head is the commit target pointed to when the run first read it.
	head string
}

// New returns the GitHub repository opts describe.
func New(opts Options) (*Host, error) {
	if opts.Token == "" {
		return nil, fmt.Errorf("%w: no token, set GITHUB_TOKEN", ErrSettings)
	}
	owner, name, ok := strings.Cut(opts.Repository, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return nil, fmt.Errorf("%w: repository %q isn't owner/name", ErrSettings, opts.Repository)
	}
	clientOpts := []gogithub.ClientOptionsFunc{gogithub.WithAuthToken(opts.Token)}
	if opts.APIURL != "" {
		clientOpts = append(clientOpts, gogithub.WithURLs(&opts.APIURL, &opts.APIURL))
	}
	client, err := gogithub.NewClient(clientOpts...)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSettings, err)
	}
	return &Host{client: client, owner: owner, name: name, target: opts.Branch}, nil
}

// EnsurePullRequest opens a pull request from the branch into the target branch,
// or brings the title and body of the open one up to date.
func (h *Host) EnsurePullRequest(
	ctx context.Context,
	request *release.PullRequest,
) (*release.PullRequestResult, error) {
	base, err := h.targetBranch(ctx)
	if err != nil {
		return nil, err
	}
	title, body := request.Title, fit(request.Body)
	existing, err := h.openPullRequest(ctx, request.Branch, base)
	if err != nil {
		return nil, err
	}

	if existing == nil {
		created, _, err := h.client.PullRequests.Create(ctx, h.owner, h.name, gogithub.CreatePullRequest{
			Title: &title, Head: request.Branch, Base: base, Body: &body,
		})
		if err != nil {
			return nil, fmt.Errorf("opening the release pull request from %s: %w", request.Branch, err)
		}
		return result(created, release.PullRequestOpened), nil
	}
	if existing.GetTitle() == title && existing.GetBody() == body {
		return result(existing, release.PullRequestUnchanged), nil
	}
	edited, _, err := h.client.PullRequests.Edit(ctx, h.owner, h.name, existing.GetNumber(), &gogithub.PullRequest{
		Title: &title, Body: &body,
	})
	if err != nil {
		return nil, fmt.Errorf("updating release pull request #%d: %w", existing.GetNumber(), err)
	}
	return result(edited, release.PullRequestUpdated), nil
}

// EnsureRelease publishes a release of the tag, titled with its name, unless it already has one.
// The release targets the release commit, so a tag that isn't pushed yet is created there rather than on the
// default branch.
func (h *Host) EnsureRelease(ctx context.Context, tag *release.Tag) (url string, created bool, err error) {
	existing, _, err := h.client.Repositories.GetReleaseByTag(ctx, h.owner, h.name, tag.Name)
	if err == nil {
		return existing.GetHTMLURL(), false, nil
	}
	if !notFound(err) {
		return "", false, fmt.Errorf("finding the release of %s: %w", tag.Name, err)
	}
	notes := fit(tag.Notes)
	published, _, err := h.client.Repositories.CreateRelease(ctx, h.owner, h.name, gogithub.CreateReleaseRequest{
		TagName: tag.Name, TargetCommitish: &tag.ReleaseCommit, Name: &tag.Name, Body: &notes,
	})
	if err != nil {
		return "", false, fmt.Errorf("publishing the release of %s: %w", tag.Name, err)
	}
	return published.GetHTMLURL(), true, nil
}

// targetBranch returns the branch releases are made from: the one Options named, or the default branch.
func (h *Host) targetBranch(ctx context.Context) (string, error) {
	if h.target != "" {
		return h.target, nil
	}
	repo, _, err := h.client.Repositories.Get(ctx, h.owner, h.name)
	if err != nil {
		return "", fmt.Errorf("reading %s/%s: %w", h.owner, h.name, err)
	}
	h.target = repo.GetDefaultBranch()
	return h.target, nil
}

// openPullRequest returns the open pull request from branch into base, or nil if there isn't one.
// GitHub allows only one open pull request between two branches, so it is the first and only result.
func (h *Host) openPullRequest(ctx context.Context, branch, base string) (*gogithub.PullRequest, error) {
	pulls, _, err := h.client.PullRequests.List(ctx, h.owner, h.name, &gogithub.PullRequestListOptions{
		State: "open", Head: h.owner + ":" + branch, Base: base,
	})
	if err != nil {
		return nil, fmt.Errorf("finding the release pull request: %w", err)
	}
	if len(pulls) == 0 {
		return nil, nil //nolint:nilnil // no pull request is a valid answer, not an error
	}
	return pulls[0], nil
}

func result(pull *gogithub.PullRequest, action release.PullRequestAction) *release.PullRequestResult {
	return &release.PullRequestResult{Number: pull.GetNumber(), URL: pull.GetHTMLURL(), Action: action}
}

func notFound(err error) bool {
	var response *gogithub.ErrorResponse
	return errors.As(err, &response) && response.Response != nil && response.Response.StatusCode == http.StatusNotFound
}

// fit cuts text down to the most GitHub allows in a body, saying it was cut.
func fit(text string) string {
	if utf8.RuneCountInString(text) <= maxBody {
		return text
	}
	return string([]rune(text)[:maxBody-utf8.RuneCountInString(truncated)]) + truncated
}
