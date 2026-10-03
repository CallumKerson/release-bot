package github

import (
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CallumKerson/release-bot/internal/git"
	"github.com/CallumKerson/release-bot/internal/github/githubtest"
	"github.com/CallumKerson/release-bot/internal/release"
	"github.com/CallumKerson/release-bot/internal/release/releasetest"
)

const repository = "octo-org/widgets"

func TestHostContract(t *testing.T) {
	releasetest.RunHostContract(t, func(t *testing.T, history ...releasetest.Commit) *releasetest.HostFixture {
		t.Helper()
		work := releasetest.NewGit(t, history...)
		origin := filepath.Join(t.TempDir(), "origin.git")
		work.Git("init", "--quiet", "--bare", origin)
		work.Git("remote", "add", "origin", origin)
		work.Git("push", "--quiet", "origin", "main")

		server := githubtest.New(t, githubtest.Options{Repository: repository, Origin: origin})
		host, err := New(Options{Token: githubtest.Token, Repository: repository, APIURL: server.URL})
		require.NoError(t, err)
		repo, err := git.Open(t.Context(), work.Dir)
		require.NoError(t, err)
		remote := repo.Remote("origin")
		return &releasetest.HostFixture{
			Repo:   repo,
			Remote: remote,
			Host:   host,
			Pushed: func(ref string) (string, bool) {
				out, err := exec.CommandContext(t.Context(), "git", "--git-dir", origin,
					"rev-parse", "--verify", "--quiet", ref+"^{commit}").Output()
				return strings.TrimSpace(string(out)), err == nil
			},
			Merge: func(t *testing.T, number int) {
				t.Helper()
				_, err := server.MergePullRequest(t.Context(), number, githubtest.Squash)
				require.NoError(t, err)
			},
		}
	})
}

func TestNewChecksTheSettings(t *testing.T) {
	tests := map[string]Options{
		"no token":           {Repository: repository},
		"no owner":           {Token: "t", Repository: "/widgets"},
		"no name":            {Token: "t", Repository: "octo-org"},
		"too many parts":     {Token: "t", Repository: "octo-org/widgets/more"},
		"unparsable API URL": {Token: "t", Repository: repository, APIURL: "://nope"},
	}
	for name, opts := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := New(opts)
			require.ErrorIs(t, err, ErrSettings)
		})
	}

	_, err := New(Options{Token: "t", Repository: repository})
	require.NoError(t, err, "api.github.com by default")
}

func TestErrorsSayWhatFailed(t *testing.T) {
	server := githubtest.New(t, githubtest.Options{Repository: repository, Origin: t.TempDir()})
	host, err := New(Options{Token: "wrong", Repository: repository, APIURL: server.URL})
	require.NoError(t, err)

	_, err = host.EnsurePullRequest(t.Context(), &release.PullRequest{Branch: "release"})
	require.ErrorContains(t, err, "reading octo-org/widgets")
	require.ErrorContains(t, err, "401 Bad credentials")

	_, _, err = host.EnsureRelease(t.Context(), &release.Tag{Name: "v1.0.0"})
	require.ErrorContains(t, err, "finding the release of v1.0.0")
	assert.Len(t, server.Writes(), 0, "nothing is written after a failed read")
	assert.Equal(t, http.MethodGet, server.Requests()[0].Method)
}

func TestFitCutsLongBodies(t *testing.T) {
	assert.Equal(t, "short", fit("short"))
	exact := strings.Repeat("é", maxBody)
	assert.Equal(t, exact, fit(exact), "the limit counts characters, not bytes")

	cut := fit(exact + "more")
	assert.Equal(t, maxBody, utf8.RuneCountInString(cut))
	assert.True(t, strings.HasSuffix(cut, truncated))
}
