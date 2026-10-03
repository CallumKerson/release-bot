// Package githubtest is a fake GitHub: an HTTP server implementing the parts of the REST API release-bot uses,
// over a bare git repository that plays the GitHub repository's git remote.
//
// Nothing tests release-bot against the real GitHub, so the fake aims to behave like it:
// requests and responses use go-github's own types, so their JSON is what the client sends and expects;
// branches and tags are looked up in the bare repository, so a pull request for a branch that hasn't been pushed fails
// as it would on GitHub; and errors carry the status codes and validation errors GitHub documents.
// A request the fake doesn't implement fails the test, rather than being answered with a guess.
package githubtest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-github/v92/github"
)

// Token is the only token the fake accepts.
const Token = "githubtest-token"

const (
	// defaultPageSize is GitHub's page size when a request doesn't ask for one.
	defaultPageSize = 30
	docsURL         = "https://docs.github.com/rest"

	stateOpen = "open"

	// Resources and codes of GitHub's validation errors.
	resourcePull    = "PullRequest"
	resourceRelease = "Release"
	codeInvalid     = "invalid"
	codeCustom      = "custom"
	codeMissing     = "missing_field"
	fieldTagName    = "tag_name"
)

// Options configure a fake.
type Options struct {
	// Repository is the repository's "owner/name".
	Repository string
	// Origin is the bare git repository that holds the repository's branches and tags.
	Origin string
	// MaxPageSize caps the size of list pages, so tests can make the client follow pagination.
	MaxPageSize int
}

// Request is a request the fake received.
type Request struct {
	Method string
	Path   string
}

// Server is a fake GitHub serving one repository.
type Server struct {
	// URL is the base URL of the REST API.
	URL string

	owner, name string
	origin      string
	maxPage     int

	mu         sync.Mutex
	pulls      []*github.PullRequest
	releases   []*github.RepositoryRelease
	requests   []Request
	unexpected []string
	nextID     int64
}

// New starts a fake GitHub, stopped when the test ends. The test fails if a request was one the fake doesn't serve.
func New(test testing.TB, opts Options) *Server {
	test.Helper()
	owner, name, ok := strings.Cut(opts.Repository, "/")
	if !ok {
		test.Fatalf("githubtest: repository %q isn't owner/name", opts.Repository)
	}
	fake := &Server{owner: owner, name: name, origin: opts.Origin, maxPage: opts.MaxPageSize}

	mux := http.NewServeMux()
	repo := "/repos/{owner}/{repo}"
	mux.HandleFunc("GET "+repo, fake.getRepository)
	mux.HandleFunc("GET "+repo+"/pulls", fake.listPulls)
	mux.HandleFunc("POST "+repo+"/pulls", fake.createPull)
	mux.HandleFunc("PATCH "+repo+"/pulls/{number}", fake.editPull)
	mux.HandleFunc("GET "+repo+"/releases/tags/{tag}", fake.getReleaseByTag)
	mux.HandleFunc("POST "+repo+"/releases", fake.createRelease)
	mux.HandleFunc("/", fake.notImplemented)

	server := httptest.NewServer(fake.authenticate(mux))
	fake.URL = server.URL + "/"
	test.Cleanup(func() {
		server.Close()
		for _, request := range fake.Unexpected() {
			test.Errorf("githubtest: unexpected request %s", request)
		}
	})
	return fake
}

// Requests returns the requests received so far, oldest first.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.requests)
}

// Writes returns the requests received so far that could change something.
func (s *Server) Writes() []Request {
	return slices.DeleteFunc(s.Requests(), func(request Request) bool { return request.Method == http.MethodGet })
}

// Unexpected returns the requests the fake doesn't serve, as "METHOD path".
func (s *Server) Unexpected() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.unexpected)
}

// PullRequests returns every pull request, oldest first, with their head and base commits as they are now.
func (s *Server) PullRequests() []*github.PullRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*github.PullRequest, 0, len(s.pulls))
	for _, pull := range s.pulls {
		out = append(out, s.present(pull))
	}
	return out
}

// Releases returns every release, oldest first.
func (s *Server) Releases() []*github.RepositoryRelease {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*github.RepositoryRelease, 0, len(s.releases))
	for _, release := range s.releases {
		copied := *release
		out = append(out, &copied)
	}
	return out
}

// authenticate records each request, and refuses those without the fake's token.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(out http.ResponseWriter, req *http.Request) {
		s.mu.Lock()
		s.requests = append(s.requests, Request{Method: req.Method, Path: req.URL.Path})
		s.mu.Unlock()
		if req.Header.Get("Authorization") != "Bearer "+Token {
			writeError(out, http.StatusUnauthorized, "Bad credentials")
			return
		}
		next.ServeHTTP(out, req)
	})
}

func (s *Server) notImplemented(out http.ResponseWriter, req *http.Request) {
	s.mu.Lock()
	s.unexpected = append(s.unexpected, req.Method+" "+req.URL.RequestURI())
	s.mu.Unlock()
	writeError(out, http.StatusNotImplemented, "githubtest doesn't implement "+req.Method+" "+req.URL.Path)
}

// thisRepository answers 404, as GitHub does, for requests about any other repository.
func (s *Server) thisRepository(out http.ResponseWriter, req *http.Request) bool {
	if req.PathValue("owner") != s.owner || req.PathValue("repo") != s.name {
		writeError(out, http.StatusNotFound, "Not Found")
		return false
	}
	return true
}

// https://docs.github.com/rest/repos/repos#get-a-repository
func (s *Server) getRepository(out http.ResponseWriter, req *http.Request) {
	if !s.thisRepository(out, req) {
		return
	}
	branch, err := s.defaultBranch(req.Context())
	if err != nil {
		writeError(out, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(out, http.StatusOK, &github.Repository{
		Name:          new(s.name),
		FullName:      new(s.owner + "/" + s.name),
		Owner:         &github.User{Login: new(s.owner)},
		DefaultBranch: new(branch),
		HTMLURL:       new(s.htmlURL()),
	})
}

// https://docs.github.com/rest/pulls/pulls#list-pull-requests
func (s *Server) listPulls(out http.ResponseWriter, req *http.Request) {
	if !s.thisRepository(out, req) {
		return
	}
	query := req.URL.Query()
	state := query.Get("state")
	if state == "" {
		state = stateOpen
	}
	head, base := query.Get("head"), query.Get("base")

	s.mu.Lock()
	var matched []*github.PullRequest
	for _, pull := range slices.Backward(s.pulls) { // newest first, GitHub's default order
		if (state == "all" || pull.GetState() == state) &&
			(head == "" || head == pull.GetHead().GetLabel()) &&
			(base == "" || base == pull.GetBase().GetRef()) {
			matched = append(matched, s.present(pull))
		}
	}
	s.mu.Unlock()
	writeJSON(out, http.StatusOK, s.paginate(out, req, matched))
}

// https://docs.github.com/rest/pulls/pulls#create-a-pull-request
func (s *Server) createPull(out http.ResponseWriter, req *http.Request) {
	if !s.thisRepository(out, req) {
		return
	}
	var body github.CreatePullRequest
	if !readJSON(out, req, &body) {
		return
	}
	head := strings.TrimPrefix(body.Head, s.owner+":")
	if body.GetTitle() == "" {
		writeValidation(out, &github.Error{Resource: resourcePull, Field: "title", Code: codeMissing})
		return
	}
	headSHA, headOK, err := s.ref(req.Context(), "refs/heads/"+head)
	if err != nil {
		writeError(out, http.StatusInternalServerError, err.Error())
		return
	}
	baseSHA, baseOK, err := s.ref(req.Context(), "refs/heads/"+body.Base)
	if err != nil {
		writeError(out, http.StatusInternalServerError, err.Error())
		return
	}
	switch {
	case !baseOK:
		writeValidation(out, &github.Error{Resource: resourcePull, Field: "base", Code: codeInvalid})
		return
	case !headOK:
		writeValidation(out, &github.Error{Resource: resourcePull, Field: "head", Code: codeInvalid})
		return
	}
	ahead, err := s.ahead(req.Context(), baseSHA, headSHA)
	if err != nil {
		writeError(out, http.StatusInternalServerError, err.Error())
		return
	}
	if !ahead {
		writeValidation(out, &github.Error{
			Resource: resourcePull, Code: codeCustom,
			Message: fmt.Sprintf("No commits between %s and %s", body.Base, head),
		})
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	label := s.owner + ":" + head
	for _, pull := range s.pulls {
		if pull.GetState() == stateOpen && pull.GetHead().GetLabel() == label && pull.GetBase().GetRef() == body.Base {
			writeValidation(out, &github.Error{
				Resource: resourcePull, Code: codeCustom,
				Message: "A pull request already exists for " + label + ".",
			})
			return
		}
	}
	s.nextID++
	number := len(s.pulls) + 1
	now := github.Timestamp{Time: time.Now().UTC()}
	pull := &github.PullRequest{
		ID:        new(s.nextID),
		Number:    new(number),
		State:     new(stateOpen),
		Title:     body.Title,
		Body:      new(body.GetBody()),
		Draft:     new(body.GetDraft()),
		Merged:    new(false),
		HTMLURL:   new(fmt.Sprintf("%s/pull/%d", s.htmlURL(), number)),
		CreatedAt: &now,
		UpdatedAt: &now,
		Head:      &github.PullRequestBranch{Ref: new(head), Label: new(label)},
		Base:      &github.PullRequestBranch{Ref: new(body.Base), Label: new(s.owner + ":" + body.Base)},
	}
	s.pulls = append(s.pulls, pull)
	writeJSON(out, http.StatusCreated, s.present(pull))
}

// pullRequestUpdate is the body of a pull request edit, as go-github sends it.
type pullRequestUpdate struct {
	Title *string `json:"title,omitempty"`
	Body  *string `json:"body,omitempty"`
	State *string `json:"state,omitempty"`
	Base  *string `json:"base,omitempty"`
}

// https://docs.github.com/rest/pulls/pulls#update-a-pull-request
func (s *Server) editPull(out http.ResponseWriter, req *http.Request) {
	if !s.thisRepository(out, req) {
		return
	}
	var update pullRequestUpdate
	if !readJSON(out, req, &update) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pull := s.pull(req.PathValue("number"))
	if pull == nil {
		writeError(out, http.StatusNotFound, "Not Found")
		return
	}
	if update.Base != nil {
		// release-bot never moves a pull request, so the fake doesn't either.
		s.unexpected = append(s.unexpected, "PATCH "+req.URL.Path+" changing the base branch")
		writeError(out, http.StatusNotImplemented, "githubtest doesn't change base branches")
		return
	}
	if update.State != nil {
		if pull.GetMerged() {
			writeValidation(out, &github.Error{
				Resource: resourcePull, Field: "state", Code: codeCustom,
				Message: "Cannot change the state of a merged pull request",
			})
			return
		}
		pull.State = update.State
	}
	if update.Title != nil {
		pull.Title = update.Title
	}
	if update.Body != nil {
		pull.Body = update.Body
	}
	pull.UpdatedAt = &github.Timestamp{Time: time.Now().UTC()}
	writeJSON(out, http.StatusOK, s.present(pull))
}

// https://docs.github.com/rest/releases/releases#get-a-release-by-tag-name
func (s *Server) getReleaseByTag(out http.ResponseWriter, req *http.Request) {
	if !s.thisRepository(out, req) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, release := range s.releases {
		if release.TagName == req.PathValue("tag") {
			writeJSON(out, http.StatusOK, release)
			return
		}
	}
	writeError(out, http.StatusNotFound, "Not Found")
}

// https://docs.github.com/rest/releases/releases#create-a-release
//
// As on GitHub, releasing a tag that doesn't exist creates it, as a lightweight tag on target_commitish,
// or the default branch when there's no target.
func (s *Server) createRelease(out http.ResponseWriter, req *http.Request) {
	if !s.thisRepository(out, req) {
		return
	}
	var body github.CreateReleaseRequest
	if !readJSON(out, req, &body) {
		return
	}
	if body.TagName == "" {
		writeValidation(out, &github.Error{Resource: resourceRelease, Field: fieldTagName, Code: codeMissing})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, release := range s.releases {
		if release.TagName == body.TagName {
			writeValidation(out, &github.Error{Resource: resourceRelease, Field: fieldTagName, Code: "already_exists"})
			return
		}
	}
	target := body.GetTargetCommitish()
	if target == "" {
		branch, err := s.defaultBranch(req.Context())
		if err != nil {
			writeError(out, http.StatusInternalServerError, err.Error())
			return
		}
		target = branch
	}
	if _, tagged, err := s.ref(req.Context(), "refs/tags/"+body.TagName); err != nil {
		writeError(out, http.StatusInternalServerError, err.Error())
		return
	} else if !tagged {
		if err := s.lightweightTag(req.Context(), body.TagName, target); err != nil {
			writeValidation(out, &github.Error{Resource: resourceRelease, Field: "target_commitish", Code: codeInvalid})
			return
		}
	}

	s.nextID++
	now := github.Timestamp{Time: time.Now().UTC()}
	release := &github.RepositoryRelease{
		ID:              s.nextID,
		TagName:         body.TagName,
		TargetCommitish: target,
		Name:            new(body.GetName()),
		Body:            new(body.GetBody()),
		Draft:           body.GetDraft(),
		Prerelease:      body.GetPrerelease(),
		CreatedAt:       now,
		PublishedAt:     &now,
		HTMLURL:         s.htmlURL() + "/releases/tag/" + body.TagName,
	}
	s.releases = append(s.releases, release)
	writeJSON(out, http.StatusCreated, release)
}

// present returns a copy of pull with the commits its branches point to now. The caller holds the lock.
func (s *Server) present(pull *github.PullRequest) *github.PullRequest {
	copied := *pull
	head, base := *pull.Head, *pull.Base
	if !pull.GetMerged() {
		if sha, ok, _ := s.ref(context.Background(), "refs/heads/"+head.GetRef()); ok {
			head.SHA = new(sha)
		}
		if sha, ok, _ := s.ref(context.Background(), "refs/heads/"+base.GetRef()); ok {
			base.SHA = new(sha)
		}
	}
	copied.Head, copied.Base = &head, &base
	return &copied
}

// pull returns the pull request numbered number, or nil. The caller holds the lock.
func (s *Server) pull(number string) *github.PullRequest {
	n, err := strconv.Atoi(number)
	if err != nil || n < 1 || n > len(s.pulls) {
		return nil
	}
	return s.pulls[n-1]
}

// paginate returns the page of items a request asks for, and links the other pages as GitHub does.
func (s *Server) paginate(
	out http.ResponseWriter,
	req *http.Request,
	items []*github.PullRequest,
) []*github.PullRequest {
	size := defaultPageSize
	if n, err := strconv.Atoi(req.URL.Query().Get("per_page")); err == nil && n > 0 {
		size = min(n, 100)
	}
	if s.maxPage > 0 {
		size = min(size, s.maxPage)
	}
	page := 1
	if n, err := strconv.Atoi(req.URL.Query().Get("page")); err == nil && n > 0 {
		page = n
	}
	last := max(1, (len(items)+size-1)/size)
	link := func(n int, rel string) string {
		query := req.URL.Query()
		query.Set("page", strconv.Itoa(n))
		query.Set("per_page", strconv.Itoa(size))
		return fmt.Sprintf(`<%s%s?%s>; rel=%q`, strings.TrimSuffix(s.URL, "/"), req.URL.Path, query.Encode(), rel)
	}
	var links []string
	if page < last {
		links = append(links, link(page+1, "next"), link(last, "last"))
	}
	if page > 1 {
		links = append(links, link(1, "first"), link(page-1, "prev"))
	}
	if len(links) > 0 {
		out.Header().Set("Link", strings.Join(links, ", "))
	}
	start := min(len(items), (page-1)*size)
	end := min(len(items), start+size)
	return items[start:end]
}

func (s *Server) htmlURL() string {
	return "https://github.com/" + s.owner + "/" + s.name
}

func readJSON(out http.ResponseWriter, req *http.Request, value any) bool {
	if err := json.NewDecoder(req.Body).Decode(value); err != nil {
		writeError(out, http.StatusBadRequest, "Problems parsing JSON")
		return false
	}
	return true
}

func writeJSON(out http.ResponseWriter, status int, value any) {
	out.Header().Set("Content-Type", "application/json; charset=utf-8")
	out.WriteHeader(status)
	_ = json.NewEncoder(out).Encode(value)
}

func writeError(out http.ResponseWriter, status int, message string) {
	writeJSON(out, status, &github.ErrorResponse{
		Message:          message,
		DocumentationURL: docsURL,
	})
}

// writeValidation answers 422 Unprocessable Entity with a validation error, as GitHub does for invalid requests.
func writeValidation(out http.ResponseWriter, detail *github.Error) {
	writeJSON(out, http.StatusUnprocessableEntity, &github.ErrorResponse{
		Message:          "Validation Failed",
		Errors:           []github.Error{*detail},
		DocumentationURL: docsURL,
	})
}
