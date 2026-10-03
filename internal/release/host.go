package release

import "context"

// Host is the code host that a run's release pull request and releases live on, such as GitHub.
type Host interface {
	// EnsurePullRequest opens the release pull request from its branch, or brings an open one up to date.
	EnsurePullRequest(ctx context.Context, pr *PullRequest) (*PullRequestResult, error)
	// EnsureRelease publishes a release of a tag, unless the tag already has one.
	EnsureRelease(ctx context.Context, tag *Tag) (url string, created bool, err error)
}

// PullRequest is the release pull request: the release branch, described by its title and body.
type PullRequest struct {
	Branch string
	Title  string
	Body   string
}

// PullRequestAction is what ensuring the release pull request did.
type PullRequestAction string

// What ensuring the release pull request can do.
const (
	PullRequestOpened    PullRequestAction = "opened"
	PullRequestUpdated   PullRequestAction = "updated"
	PullRequestUnchanged PullRequestAction = "unchanged"
)

// PullRequestResult is the release pull request on the host, and what ensuring it did.
type PullRequestResult struct {
	Number int               `json:"number"`
	URL    string            `json:"url"`
	Action PullRequestAction `json:"action"`
}
