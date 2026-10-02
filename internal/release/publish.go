package release

import (
	"context"
	"slices"
	"strings"
)

// Published is what Publish did on the remote and the host.
type Published struct {
	// Releases are the releases of the current versions, each published now or found already published.
	Releases []PublishedRelease `json:"releases,omitempty"`
	// BranchPushed is false when the remote already had the release branch, or there is none.
	BranchPushed bool               `json:"branch_pushed"`
	PullRequest  *PullRequestResult `json:"pull_request,omitempty"`
}

// PublishedRelease is a release on the host.
type PublishedRelease struct {
	Tag string `json:"tag"`
	URL string `json:"url"`
	// Created is false when the release was already published.
	Created bool `json:"created"`
}

// Publish makes what Apply did public: it pushes the tags and publishes a release of each current version,
// then pushes the release branch and opens or updates its pull request.
//
// Every current version gets a release, not just the ones tagged now, so a run that stopped part way through
// is finished by the next. Tags are pushed before their releases, and the branch before its pull request,
// because the host can only refer to what it has.
func Publish(ctx context.Context, remote Remote, host Host, result *Result, outcome *Outcome) (*Published, error) {
	current := slices.Concat(result.Tagged, result.Untagged)
	slices.SortFunc(current, func(a, b Tag) int { return strings.Compare(a.Package, b.Package) })
	names := make([]string, 0, len(current))
	for _, tag := range current {
		names = append(names, tag.Name)
	}
	if err := remote.PushTags(ctx, names); err != nil {
		return nil, err
	}

	published := &Published{}
	for _, tag := range current {
		url, created, err := host.EnsureRelease(ctx, &tag)
		if err != nil {
			return nil, err
		}
		published.Releases = append(published.Releases, PublishedRelease{Tag: tag.Name, URL: url, Created: created})
	}

	if result.Branch == nil {
		return published, nil
	}
	pushed, err := remote.PushBranch(ctx, result.Branch.Name, outcome.BranchCommit)
	if err != nil {
		return nil, err
	}
	published.BranchPushed = pushed
	published.PullRequest, err = host.EnsurePullRequest(ctx, &PullRequest{
		Branch: result.Branch.Name, Title: result.Branch.Title, Body: result.Branch.Body,
	})
	if err != nil {
		return nil, err
	}
	return published, nil
}
