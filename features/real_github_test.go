package features

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"
	gogithub "github.com/google/go-github/v92/github"

	"github.com/CallumKerson/release-bot/internal/testing/gitrepo"
)

// End-to-end mode runs the GitHub scenarios against a real GitHub repository, which each scenario resets:
// it closes the open pull requests, deletes every release, and force pushes the scenario's branches and tags,
// deleting the rest. Only use a repository that exists for this.
//
//	RELEASE_BOT_E2E_REPOSITORY=owner/name go test ./features -run TestEndToEnd -count=1
//
// It runs as a GitHub App installed on the repository, with read and write access to contents and pull requests,
// whose client ID and private key are RELEASE_BOT_E2E_APP_CLIENT_ID and RELEASE_BOT_E2E_APP_PRIVATE_KEY,
// as release-bot runs as an app or a workflow's GITHUB_TOKEN, and GitHub only signs its commits for those.
// `mise run e2e` reads them from 1Password with fnox. RELEASE_BOT_E2E_TOKEN runs with a token instead.

const (
	// e2eAPIURL is the REST API end-to-end mode uses.
	e2eAPIURL = "https://api.github.com/"
	// appJWTLifetime is how long the app's JWT lasts, under GitHub's limit of ten minutes.
	appJWTLifetime = 9 * time.Minute
)

var (
	// errMergeTimeout is returned when GitHub won't merge a pull request for longer than mergeAttempts tries.
	errMergeTimeout = errors.New("GitHub didn't merge the pull request in time")
	// errPrivateKey is returned for a GitHub App private key that isn't an RSA key in PEM.
	errPrivateKey = errors.New("the GitHub App private key isn't an RSA private key in PEM")
)

// flatPEM matches a PEM block on one line: its header, its base64 body, and its footer.
var flatPEM = regexp.MustCompile(`^(-{5}BEGIN [A-Z ]+-{5}) (.+) (-{5}END [A-Z ]+-{5})$`)

// pullNumber matches a pull request number, as release-bot writes it or in its URL.
var pullNumber = regexp.MustCompile(`(#|/pull/)(\d+)\b`)

func TestEndToEnd(t *testing.T) {
	repo := os.Getenv("RELEASE_BOT_E2E_REPOSITORY")
	if repo == "" {
		t.Skip("set RELEASE_BOT_E2E_REPOSITORY to the GitHub repository to run against, which will be reset")
	}
	owner, name, ok := strings.Cut(repo, "/")
	if !ok {
		t.Fatalf("RELEASE_BOT_E2E_REPOSITORY %q isn't owner/name", repo)
	}
	token := e2eToken(t, owner, name)
	client, err := gogithub.NewClient(gogithub.WithAuthToken(token))
	if err != nil {
		t.Fatal(err)
	}
	newGitHub := func(ctx context.Context) (gitHub, error) {
		return newRealGitHub(ctx, &realGitHub{client: client, owner: owner, name: name, token: token})
	}

	gitrepo.Isolate(t)
	opts := godogOptions
	opts.TestingT = t
	opts.Paths = []string{"github.feature"}
	suite := godog.TestSuite{Name: "release-bot-e2e", ScenarioInitializer: scenarios(newGitHub), Options: &opts}
	if suite.Run() != 0 {
		t.Fatal("feature scenarios failed")
	}
}

// e2eToken is RELEASE_BOT_E2E_TOKEN, or a new installation token of the GitHub App on the repository.
// An installation token lasts an hour, longer than the scenarios take.
func e2eToken(t *testing.T, owner, name string) string {
	t.Helper()
	if token := os.Getenv("RELEASE_BOT_E2E_TOKEN"); token != "" {
		return token
	}
	clientID, privateKey := os.Getenv("RELEASE_BOT_E2E_APP_CLIENT_ID"), os.Getenv("RELEASE_BOT_E2E_APP_PRIVATE_KEY")
	if clientID == "" || privateKey == "" {
		t.Fatal("no token: set RELEASE_BOT_E2E_APP_CLIENT_ID and RELEASE_BOT_E2E_APP_PRIVATE_KEY, " +
			"or RELEASE_BOT_E2E_TOKEN, or run with `mise run e2e`")
	}
	token, err := installationToken(t.Context(), clientID, privateKey, owner, name)
	if err != nil {
		t.Fatalf("can't get a token for the GitHub App: %v", err)
	}
	return token
}

// installationToken returns a new token for the GitHub App's installation on owner/name, limited to that repository.
// https://docs.github.com/apps/creating-github-apps/authenticating-with-a-github-app/authenticating-as-a-github-app-installation
func installationToken(ctx context.Context, clientID, privateKey, owner, name string) (string, error) {
	jwt, err := appJWT(clientID, privateKey, time.Now())
	if err != nil {
		return "", err
	}
	app, err := gogithub.NewClient(gogithub.WithAuthToken(jwt))
	if err != nil {
		return "", err
	}
	installation, _, err := app.Apps.GetRepositoryInstallation(ctx, owner, name)
	if err != nil {
		return "", err
	}
	token, _, err := app.Apps.CreateInstallationToken(ctx, installation.GetID(),
		&gogithub.InstallationTokenOptions{Repositories: []string{name}})
	return token.GetToken(), err
}

// appJWT returns a JWT that authenticates as the GitHub App, signed with its private key.
// It is issued a minute early, as GitHub recommends, in case the clocks differ.
// https://docs.github.com/apps/creating-github-apps/authenticating-with-a-github-app/generating-a-json-web-token-jwt-for-a-github-app
func appJWT(clientID, privateKey string, now time.Time) (string, error) {
	block, _ := pem.Decode([]byte(privateKey))
	if block == nil {
		// A key kept in a single-line field has spaces where its line breaks were.
		if parts := flatPEM.FindStringSubmatch(strings.TrimSpace(privateKey)); parts != nil {
			block, _ = pem.Decode([]byte(parts[1] + "\n" + strings.ReplaceAll(parts[2], " ", "\n") + "\n" + parts[3]))
		}
	}
	if block == nil {
		return "", errPrivateKey
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		parsed, pkcs8Err := x509.ParsePKCS8PrivateKey(block.Bytes)
		var ok bool
		if key, ok = parsed.(*rsa.PrivateKey); pkcs8Err != nil || !ok {
			return "", errPrivateKey
		}
	}
	claims, err := json.Marshal(map[string]any{
		"iat": now.Add(-time.Minute).Unix(), "exp": now.Add(appJWTLifetime).Unix(), "iss": clientID,
	})
	if err != nil {
		return "", err
	}
	encode := base64.RawURLEncoding.EncodeToString
	unsigned := encode([]byte(`{"alg":"RS256","typ":"JWT"}`)) + "." + encode(claims)
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(nil, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return unsigned + "." + encode(signature), nil
}

// realGitHub is a real GitHub repository.
type realGitHub struct {
	client      *gogithub.Client
	owner, name string
	token       string
	// pullsBefore is the highest issue or pull request number before the scenario,
	// which readable takes from later numbers so that the scenario's first pull request reads as #1, as on the fake.
	pullsBefore int
}

// newRealGitHub resets the repository for a scenario, except for its branches and tags, which onGitHub replaces.
func newRealGitHub(ctx context.Context, hub *realGitHub) (gitHub, error) {
	open, _, err := hub.client.PullRequests.List(ctx, hub.owner, hub.name,
		&gogithub.PullRequestListOptions{State: "open", PerPage: 100})
	if err != nil {
		return nil, err
	}
	for _, pull := range open {
		if _, _, err := hub.client.PullRequests.Edit(ctx, hub.owner, hub.name, pull.GetNumber(),
			&gogithub.PullRequest{State: new("closed")}); err != nil {
			return nil, err
		}
	}
	releases, _, err := hub.client.Repositories.ListReleases(ctx, hub.owner, hub.name,
		&gogithub.ListOptions{PerPage: 100})
	if err != nil {
		return nil, err
	}
	for _, release := range releases {
		if _, err := hub.client.Repositories.DeleteRelease(ctx, hub.owner, hub.name, release.GetID()); err != nil {
			return nil, err
		}
	}
	latest, _, err := hub.client.Issues.ListByRepo(ctx, hub.owner, hub.name, &gogithub.IssueListByRepoOptions{
		State: "all", Sort: "created", Direction: "desc", ListOptions: gogithub.ListOptions{PerPage: 1},
	})
	if err != nil {
		return nil, err
	}
	if len(latest) > 0 {
		hub.pullsBefore = latest[0].GetNumber()
	}
	return hub, nil
}

func (r *realGitHub) remote() (url string, config map[string]string) {
	credentials := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + r.token))
	return fmt.Sprintf("https://github.com/%s/%s.git", r.owner, r.name), map[string]string{
		"http.https://github.com/.extraheader": "AUTHORIZATION: basic " + credentials,
	}
}

func (r *realGitHub) env() map[string]string {
	return map[string]string{
		"GITHUB_TOKEN":      r.token,
		"GITHUB_REPOSITORY": r.owner + "/" + r.name,
		"GITHUB_API_URL":    e2eAPIURL,
	}
}

func (r *realGitHub) publishRelease(ctx context.Context, tag, notes string) error {
	_, _, err := r.client.Repositories.CreateRelease(ctx, r.owner, r.name,
		gogithub.CreateReleaseRequest{TagName: tag, Name: new(tag), Body: new(notes)})
	return err
}

// mergeAttempts is how many times mergePullRequest tries, a second apart,
// as GitHub can refuse to merge a pull request until it has worked out whether it can.
const mergeAttempts = 10

func (r *realGitHub) mergePullRequest(ctx context.Context, number int, method string) (string, error) {
	for range mergeAttempts {
		merged, resp, err := r.client.PullRequests.Merge(ctx, r.owner, r.name, number, "",
			&gogithub.PullRequestOptions{MergeMethod: method})
		if err == nil {
			return merged.GetSHA(), nil
		}
		if resp == nil || resp.StatusCode != http.StatusMethodNotAllowed {
			return "", err
		}
		time.Sleep(time.Second)
	}
	return "", fmt.Errorf("%w: #%d", errMergeTimeout, number)
}

func (r *realGitHub) verified(ctx context.Context, commit string) (bool, error) {
	got, _, err := r.client.Git.GetCommit(ctx, r.owner, r.name, commit)
	if err != nil {
		return false, err
	}
	return got.GetVerification().GetVerified(), nil
}

func (r *realGitHub) openPullRequests(ctx context.Context) ([]pullRequest, error) {
	pulls, _, err := r.client.PullRequests.List(ctx, r.owner, r.name,
		&gogithub.PullRequestListOptions{State: "open", PerPage: 100})
	open := make([]pullRequest, 0, len(pulls))
	for _, pull := range pulls {
		open = append(open, pullRequest{pull.GetNumber(), pull.GetTitle(), pull.GetBody()})
	}
	return open, err
}

func (r *realGitHub) releases(ctx context.Context) ([]release, error) {
	published, _, err := r.client.Repositories.ListReleases(ctx, r.owner, r.name, &gogithub.ListOptions{PerPage: 100})
	out := make([]release, 0, len(published))
	for _, each := range published {
		out = append(out, release{each.GetTagName(), each.GetBody()})
	}
	return out, err
}

// activity lists the open pull requests and the releases, with when each last changed.
func (r *realGitHub) activity(ctx context.Context) (string, error) {
	pulls, _, err := r.client.PullRequests.List(ctx, r.owner, r.name,
		&gogithub.PullRequestListOptions{State: "all", PerPage: 100})
	if err != nil {
		return "", err
	}
	var lines []string
	for _, pull := range pulls {
		if pull.GetNumber() > r.pullsBefore {
			lines = append(lines, fmt.Sprintf("pull request #%d %s, updated %s",
				pull.GetNumber(), pull.GetState(), pull.GetUpdatedAt().Format(time.RFC3339)))
		}
	}
	releases, _, err := r.client.Repositories.ListReleases(ctx, r.owner, r.name, &gogithub.ListOptions{PerPage: 100})
	for _, release := range releases {
		lines = append(lines, fmt.Sprintf("release %d of %s", release.GetID(), release.GetTagName()))
	}
	return r.readable(strings.Join(lines, "\n")), err
}

// readable names the repository and numbers its pull requests as the fake GitHub does.
func (r *realGitHub) readable(text string) string {
	text = strings.ReplaceAll(text, r.owner+"/"+r.name, repository)
	return pullNumber.ReplaceAllStringFunc(text, func(match string) string {
		parts := pullNumber.FindStringSubmatch(match)
		number, err := strconv.Atoi(parts[2])
		if err != nil || number <= r.pullsBefore {
			return match
		}
		return parts[1] + strconv.Itoa(number-r.pullsBefore)
	})
}

// patience covers GitHub's lists lagging, such as a release not being listed straight after it is published.
func (r *realGitHub) patience() time.Duration {
	return 10 * time.Second
}

// close leaves the repository as the scenario left it, to look at when the scenario fails.
func (r *realGitHub) close() error {
	return nil
}
