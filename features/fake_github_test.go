package features

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/CallumKerson/release-bot/internal/github/githubtest"
)

// repository is the fake GitHub repository every scenario uses.
const repository = "octo-org/widgets"

// fakeGitHub is a fake GitHub, running in process over a bare git repository, so the scenarios need no network or
// account.
type fakeGitHub struct {
	server *githubtest.Server
	// origin is the bare git repository behind the fake.
	origin string
}

func newFakeGitHub(ctx context.Context) (gitHub, error) {
	dir, err := os.MkdirTemp("", "release-bot-origin-")
	if err != nil {
		return nil, err
	}
	origin := filepath.Join(dir, "origin.git")
	if _, err := git(ctx, dir, nil, "init", "--quiet", "--bare", origin); err != nil {
		return nil, err
	}
	server, err := githubtest.Start(githubtest.Options{Repository: repository, Origin: origin})
	if err != nil {
		return nil, err
	}
	return &fakeGitHub{server: server, origin: origin}, nil
}

func (f *fakeGitHub) remote() (url string, config map[string]string) {
	return f.origin, nil
}

func (f *fakeGitHub) env() map[string]string {
	return map[string]string{
		"GITHUB_TOKEN":      githubtest.Token,
		"GITHUB_REPOSITORY": repository,
		"GITHUB_API_URL":    f.server.URL,
	}
}

func (f *fakeGitHub) publishRelease(_ context.Context, tag, notes string) error {
	f.server.PublishRelease(tag, notes)
	return nil
}

func (f *fakeGitHub) mergePullRequest(ctx context.Context, number int, method string) (string, error) {
	return f.server.MergePullRequest(ctx, number, method)
}

func (f *fakeGitHub) verified(_ context.Context, commit string) (bool, error) {
	return f.server.Verified(commit), nil
}

func (f *fakeGitHub) openPullRequests(context.Context) ([]pullRequest, error) {
	var open []pullRequest
	for _, pull := range f.server.PullRequests() {
		if pull.GetState() == "open" {
			open = append(open, pullRequest{pull.GetNumber(), pull.GetTitle(), pull.GetBody()})
		}
	}
	return open, nil
}

func (f *fakeGitHub) releases(context.Context) ([]release, error) {
	releases := f.server.Releases()
	out := make([]release, 0, len(releases))
	for _, published := range releases {
		out = append(out, release{published.TagName, published.GetBody()})
	}
	return out, nil
}

// activity lists every request that could have changed something, even one that left things as they were.
func (f *fakeGitHub) activity(context.Context) (string, error) {
	requests := f.server.Writes()
	writes := make([]string, 0, len(requests))
	for _, write := range requests {
		writes = append(writes, write.Method+" "+write.Path)
	}
	return fmt.Sprintf("%d requests that could change something\n%s", len(writes), strings.Join(writes, "\n")), nil
}

func (f *fakeGitHub) patience() time.Duration {
	return 0
}

func (f *fakeGitHub) readable(text string) string {
	return text
}

func (f *fakeGitHub) close() error {
	return errors.Join(f.server.Close(), os.RemoveAll(filepath.Dir(f.origin)))
}
