package release

import (
	"context"
	"slices"
	"strings"
)

// Published is what Publish did on the host.
type Published struct {
	// Releases are the releases of the current versions, each published now or found already published.
	Releases    []PublishedRelease `json:"releases,omitempty"`
	PullRequest *PullRequestResult `json:"pull_request,omitempty"`
}

// PublishedRelease is a release on the host.
type PublishedRelease struct {
	Tag string `json:"tag"`
	URL string `json:"url"`
	// Created is false when the release was already published.
	Created bool `json:"created"`
}

// Publish makes what Apply did public, when the repository Apply wrote to is the host's:
// it publishes a release of each current version, then opens or updates the release branch's pull request.
//
// Every current version gets a release, not just the ones tagged now, so a run that stopped part way through
// is finished by the next.
func Publish(ctx context.Context, host Host, result *Result) (*Published, error) {
	current := slices.Concat(result.Tagged, result.Untagged)
	slices.SortFunc(current, func(a, b Tag) int { return strings.Compare(a.Package, b.Package) })

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
	var err error
	published.PullRequest, err = host.EnsurePullRequest(ctx, &PullRequest{
		Branch: result.Branch.Name, Title: result.Branch.Title, Body: result.Branch.Body,
	})
	if err != nil {
		return nil, err
	}
	return published, nil
}
