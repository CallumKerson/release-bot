package githubtest

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/go-github/v92/github"
)

// The identity GitHub gives what a GITHUB_TOKEN creates through the API, when the request doesn't name one.
const (
	botName  = "github-actions[bot]"
	botEmail = "41898282+github-actions[bot]@users.noreply.github.com"
)

const (
	// maxContentSize is the largest file the contents API returns the content of; larger files have encoding "none".
	maxContentSize = 1024 * 1024
	// base64Line is the length of the lines GitHub wraps base64 content at.
	base64Line = 60
	// commitFilesPage is how many files GitHub lists per page of a commit, by default.
	commitFilesPage = 300
	// comparePage is how many commits GitHub lists per page of a comparison, by default.
	comparePage = 250

	typeCommit = "commit"
	typeTag    = "tag"
	typeTree   = "tree"
	typeBlob   = "blob"

	resourceTag    = "Tag"
	resourceCommit = "Commit"
	fieldMessage   = "message"

	rawMediaType = "application/vnd.github.v3.raw"
	fieldSep     = "\x1f"
)

// https://docs.github.com/rest/git/refs#get-a-reference
func (s *Server) getRef(out http.ResponseWriter, req *http.Request) {
	if !s.thisRepository(out, req) {
		return
	}
	ref := "refs/" + req.PathValue("ref")
	object, ok, err := s.object(req.Context(), ref)
	if err != nil {
		writeError(out, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(out, http.StatusNotFound, "Not Found")
		return
	}
	s.writeRef(req.Context(), out, http.StatusOK, ref, object)
}

// https://docs.github.com/rest/git/refs#create-a-reference
func (s *Server) createRef(out http.ResponseWriter, req *http.Request) {
	if !s.thisRepository(out, req) {
		return
	}
	var body github.CreateRef
	if !readJSON(out, req, &body) {
		return
	}
	if !strings.HasPrefix(body.Ref, "refs/") || strings.Count(body.Ref, "/") < 2 {
		writeError(
			out,
			http.StatusUnprocessableEntity,
			"Reference name must start with 'refs/' and have at least two slashes.",
		)
		return
	}
	if _, ok, err := s.object(req.Context(), body.SHA); err != nil || !ok {
		writeError(out, http.StatusUnprocessableEntity, "Object does not exist")
		return
	}
	if _, exists, err := s.object(req.Context(), body.Ref); err != nil || exists {
		writeError(out, http.StatusUnprocessableEntity, "Reference already exists")
		return
	}
	if _, err := git(req.Context(), s.origin, nil, "update-ref", body.Ref, body.SHA, ""); err != nil {
		writeError(out, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeRef(req.Context(), out, http.StatusCreated, body.Ref, body.SHA)
}

// https://docs.github.com/rest/git/refs#update-a-reference
//
// Without force, only a fast-forward is allowed, as on GitHub.
func (s *Server) updateRef(out http.ResponseWriter, req *http.Request) {
	if !s.thisRepository(out, req) {
		return
	}
	var body github.UpdateRef
	if !readJSON(out, req, &body) {
		return
	}
	ref := "refs/" + req.PathValue("ref")
	current, ok, err := s.object(req.Context(), ref)
	if err != nil {
		writeError(out, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(out, http.StatusUnprocessableEntity, "Reference does not exist")
		return
	}
	if _, ok, err := s.object(req.Context(), body.SHA); err != nil || !ok {
		writeError(out, http.StatusUnprocessableEntity, "Object does not exist")
		return
	}
	if !body.GetForce() {
		if _, err := git(req.Context(), s.origin, nil, "merge-base", "--is-ancestor", current, body.SHA); err != nil {
			writeError(out, http.StatusUnprocessableEntity, "Update is not a fast forward")
			return
		}
	}
	if _, err := git(req.Context(), s.origin, nil, "update-ref", ref, body.SHA, current); err != nil {
		writeError(out, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeRef(req.Context(), out, http.StatusOK, ref, body.SHA)
}

// https://docs.github.com/rest/git/tags#get-a-tag
func (s *Server) getTag(out http.ResponseWriter, req *http.Request) {
	if !s.thisRepository(out, req) {
		return
	}
	tag, ok, err := s.tagObject(req.Context(), req.PathValue("sha"))
	if err != nil {
		writeError(out, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(out, http.StatusNotFound, "Not Found")
		return
	}
	writeJSON(out, http.StatusOK, tag)
}

// https://docs.github.com/rest/git/tags#create-a-tag-object
//
// Like GitHub, it only creates the tag object: a tag ref pointing at it is created separately.
func (s *Server) createTag(out http.ResponseWriter, req *http.Request) {
	if !s.thisRepository(out, req) {
		return
	}
	var body github.CreateTag
	if !readJSON(out, req, &body) {
		return
	}
	switch {
	case body.Tag == "":
		writeValidation(out, &github.Error{Resource: resourceTag, Field: typeTag, Code: codeMissing})
		return
	case body.Message == "":
		writeValidation(out, &github.Error{Resource: resourceTag, Field: fieldMessage, Code: codeMissing})
		return
	}
	if kind, ok, err := s.objectType(req.Context(), body.Object); err != nil || !ok || kind != body.Type {
		writeError(out, http.StatusUnprocessableEntity, "Object does not exist")
		return
	}
	tagger := identity(body.Tagger, botName, botEmail)
	text := fmt.Sprintf("object %s\ntype %s\ntag %s\ntagger %s\n\n%s\n",
		body.Object, body.Type, body.Tag, signature(tagger), strings.TrimRight(body.Message, "\n"))
	sha, err := gitInput(req.Context(), s.origin, nil, text, "mktag")
	if err != nil {
		writeError(out, http.StatusUnprocessableEntity, err.Error())
		return
	}
	tag, _, err := s.tagObject(req.Context(), sha)
	if err != nil {
		writeError(out, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(out, http.StatusCreated, tag)
}

// https://docs.github.com/rest/git/commits#get-a-commit-object
func (s *Server) getGitCommit(out http.ResponseWriter, req *http.Request) {
	if !s.thisRepository(out, req) {
		return
	}
	commit, ok, err := s.commitObject(req.Context(), req.PathValue("sha"))
	if err != nil {
		writeError(out, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(out, http.StatusNotFound, "Not Found")
		return
	}
	writeJSON(out, http.StatusOK, commit)
}

// newCommit is the body of a commit creation, as go-github sends it.
type newCommit struct {
	Author    *github.CommitAuthor `json:"author,omitempty"`
	Committer *github.CommitAuthor `json:"committer,omitempty"`
	Message   *string              `json:"message,omitempty"`
	Tree      *string              `json:"tree,omitempty"`
	Parents   []string             `json:"parents,omitempty"`
	Signature *string              `json:"signature,omitempty"`
}

// https://docs.github.com/rest/git/commits#create-a-commit
//
// A commit without an author, committer or signature is made by the token's bot and signed by GitHub,
// so it is verified, as "Signature verification for bots" describes in
// https://docs.github.com/authentication/managing-commit-signature-verification/about-commit-signature-verification
func (s *Server) createGitCommit(out http.ResponseWriter, req *http.Request) {
	if !s.thisRepository(out, req) {
		return
	}
	var body newCommit
	if !readJSON(out, req, &body) {
		return
	}
	switch {
	case body.Message == nil:
		writeValidation(out, &github.Error{Resource: resourceCommit, Field: fieldMessage, Code: codeMissing})
		return
	case body.Tree == nil:
		writeValidation(out, &github.Error{Resource: resourceCommit, Field: "tree", Code: codeMissing})
		return
	}
	args, problem := s.commitTreeArgs(req.Context(), *body.Tree, body.Parents)
	if problem != "" {
		writeError(out, http.StatusUnprocessableEntity, problem)
		return
	}
	signed := body.Author == nil && body.Committer == nil && body.Signature == nil
	sha, err := gitInput(req.Context(), s.origin, commitIdentity(&body, signed), *body.Message,
		append(args, "-F", "-")...)
	if err != nil {
		writeError(out, http.StatusInternalServerError, err.Error())
		return
	}
	if signed {
		s.mu.Lock()
		s.signed[sha] = true
		s.mu.Unlock()
	}
	commit, _, err := s.commitObject(req.Context(), sha)
	if err != nil {
		writeError(out, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(out, http.StatusCreated, commit)
}

// newTree is the body of a tree creation, as go-github sends it.
type newTree struct {
	BaseTree string `json:"base_tree,omitempty"`
	Entries  []struct {
		Path    string  `json:"path"`
		Mode    string  `json:"mode"`
		Type    string  `json:"type"`
		SHA     *string `json:"sha"`
		Content *string `json:"content"`
	} `json:"tree"`
}

// https://docs.github.com/rest/git/trees#create-a-tree
//
// Each entry sets a path to new content or an existing object, or deletes it when it has neither.
func (s *Server) createTree(out http.ResponseWriter, req *http.Request) {
	if !s.thisRepository(out, req) {
		return
	}
	var body newTree
	if !readJSON(out, req, &body) {
		return
	}
	dir, err := os.MkdirTemp("", "githubtest-index-")
	if err != nil {
		writeError(out, http.StatusInternalServerError, err.Error())
		return
	}
	defer os.RemoveAll(dir)
	env := []string{"GIT_INDEX_FILE=" + filepath.Join(dir, "index")}
	if body.BaseTree != "" {
		if kind, ok, err := s.objectType(req.Context(), body.BaseTree); err != nil || !ok || kind != typeTree {
			writeError(out, http.StatusUnprocessableEntity, "Invalid tree info")
			return
		}
		if _, err := git(req.Context(), s.origin, env, "read-tree", body.BaseTree); err != nil {
			writeError(out, http.StatusInternalServerError, err.Error())
			return
		}
	}
	for _, entry := range body.Entries {
		var err error
		switch {
		case entry.Content != nil:
			var blob string
			if blob, err = gitInput(
				req.Context(),
				s.origin,
				nil,
				*entry.Content,
				"hash-object",
				"-w",
				"--stdin",
			); err == nil {
				_, err = git(req.Context(), s.origin, env, "update-index", "--add", "--cacheinfo",
					entry.Mode+","+blob+","+entry.Path)
			}
		case entry.SHA != nil:
			_, err = git(req.Context(), s.origin, env, "update-index", "--add", "--cacheinfo",
				entry.Mode+","+*entry.SHA+","+entry.Path)
		default:
			_, err = gitInput(req.Context(), s.origin, env, "0 "+strings.Repeat("0", 40)+"\t"+entry.Path+"\n",
				"update-index", "--index-info")
		}
		if err != nil {
			writeError(out, http.StatusUnprocessableEntity, "Invalid tree info: "+err.Error())
			return
		}
	}
	tree, err := git(req.Context(), s.origin, env, "write-tree")
	if err != nil {
		writeError(out, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(out, http.StatusCreated, &github.Tree{SHA: new(tree), Truncated: new(false)})
}

// https://docs.github.com/rest/git/blobs#get-a-blob
func (s *Server) getBlob(out http.ResponseWriter, req *http.Request) {
	if !s.thisRepository(out, req) {
		return
	}
	sha := req.PathValue("sha")
	if kind, ok, err := s.objectType(req.Context(), sha); err != nil || !ok || kind != typeBlob {
		writeError(out, http.StatusNotFound, "Not Found")
		return
	}
	content, err := gitBytes(req.Context(), s.origin, "cat-file", typeBlob, sha)
	if err != nil {
		writeError(out, http.StatusInternalServerError, err.Error())
		return
	}
	if req.Header.Get("Accept") == rawMediaType {
		out.Header().Set("Content-Type", rawMediaType)
		out.WriteHeader(http.StatusOK)
		_, _ = out.Write(content) //nolint:gosec // raw blob content, served as GitHub's raw media type rather than HTML
		return
	}
	writeJSON(out, http.StatusOK, &github.Blob{
		SHA: new(sha), Size: new(len(content)), Encoding: new("base64"), Content: new(wrapBase64(content)),
	})
}

// https://docs.github.com/rest/repos/contents#get-repository-content
//
// Only files are served: release-bot never lists a directory.
func (s *Server) getContents(out http.ResponseWriter, req *http.Request) {
	if !s.thisRepository(out, req) {
		return
	}
	ref, ok := s.revision(out, req, req.URL.Query().Get("ref"))
	if !ok {
		return
	}
	path := req.PathValue("path")
	object := ref + ":" + path
	kind, found, err := s.objectType(req.Context(), object)
	if err != nil {
		writeError(out, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		writeError(out, http.StatusNotFound, "Not Found")
		return
	}
	if kind != typeBlob {
		s.notImplemented(out, req)
		return
	}
	sha, err := git(req.Context(), s.origin, nil, "rev-parse", object)
	if err != nil {
		writeError(out, http.StatusInternalServerError, err.Error())
		return
	}
	content, err := gitBytes(req.Context(), s.origin, "cat-file", typeBlob, sha)
	if err != nil {
		writeError(out, http.StatusInternalServerError, err.Error())
		return
	}
	file := &github.RepositoryContent{
		Type: new("file"), Name: new(filepath.Base(path)), Path: new(path), SHA: new(sha), Size: new(len(content)),
		Encoding: new("base64"), Content: new(wrapBase64(content)),
	}
	if len(content) > maxContentSize {
		file.Encoding, file.Content = new("none"), new("")
	}
	writeJSON(out, http.StatusOK, file)
}

// https://docs.github.com/rest/commits/commits#list-commits
//
// Like git log, which GitHub follows, a path limits the list to the commits that changed it.
func (s *Server) listCommits(out http.ResponseWriter, req *http.Request) {
	if !s.thisRepository(out, req) {
		return
	}
	query := req.URL.Query()
	head, ok := s.revision(out, req, query.Get("sha"))
	if !ok {
		return
	}
	args := []string{"log", "--format=%H", head}
	if path := query.Get("path"); path != "" {
		args = append(args, "--", path)
	}
	shas, err := git(req.Context(), s.origin, nil, args...)
	if err != nil {
		writeError(out, http.StatusInternalServerError, err.Error())
		return
	}
	page := paginate(s, out, req, strings.Fields(shas), defaultPageSize)
	commits := make([]*github.RepositoryCommit, 0, len(page))
	for _, sha := range page {
		commit, err := s.repositoryCommit(req.Context(), sha)
		if err != nil {
			writeError(out, http.StatusInternalServerError, err.Error())
			return
		}
		commits = append(commits, commit)
	}
	writeJSON(out, http.StatusOK, commits)
}

// https://docs.github.com/rest/commits/commits#get-a-commit
//
// Files are compared with the first parent, and renames are detected, as GitHub shows them.
func (s *Server) getCommit(out http.ResponseWriter, req *http.Request) {
	if !s.thisRepository(out, req) {
		return
	}
	sha, ok := s.revision(out, req, req.PathValue("ref"))
	if !ok {
		return
	}
	commit, err := s.repositoryCommit(req.Context(), sha)
	if err != nil {
		writeError(out, http.StatusInternalServerError, err.Error())
		return
	}
	parent := ""
	if len(commit.Parents) > 0 {
		parent = commit.Parents[0].GetSHA()
	}
	files, err := s.diffFiles(req.Context(), parent, sha)
	if err != nil {
		writeError(out, http.StatusInternalServerError, err.Error())
		return
	}
	commit.Files = paginate(s, out, req, files, commitFilesPage)
	writeJSON(out, http.StatusOK, commit)
}

// https://docs.github.com/rest/commits/commits#compare-two-commits
//
// The commits are those reachable from head but not from the merge base, oldest first.
func (s *Server) compareCommits(out http.ResponseWriter, req *http.Request) {
	if !s.thisRepository(out, req) {
		return
	}
	baseName, headName, found := strings.Cut(req.PathValue("basehead"), "...")
	if !found {
		writeError(out, http.StatusNotFound, "Not Found")
		return
	}
	base, ok := s.revision(out, req, baseName)
	if !ok {
		return
	}
	head, ok := s.revision(out, req, headName)
	if !ok {
		return
	}
	mergeBase, err := git(req.Context(), s.origin, nil, "merge-base", base, head)
	if err != nil {
		writeError(out, http.StatusNotFound, "No common ancestor between "+baseName+" and "+headName+".")
		return
	}
	shas, err := git(req.Context(), s.origin, nil, "log", "--reverse", "--format=%H", mergeBase+".."+head)
	if err != nil {
		writeError(out, http.StatusInternalServerError, err.Error())
		return
	}
	ahead := strings.Fields(shas)
	behind, err := git(req.Context(), s.origin, nil, "rev-list", "--count", mergeBase+".."+base)
	if err != nil {
		writeError(out, http.StatusInternalServerError, err.Error())
		return
	}
	behindBy, _ := strconv.Atoi(behind)
	comparison := &github.CommitsComparison{
		AheadBy: new(len(ahead)), BehindBy: new(behindBy), TotalCommits: new(len(ahead)),
		Status: new(compareStatus(len(ahead), behindBy)),
	}
	for _, sha := range paginate(s, out, req, ahead, comparePage) {
		commit, err := s.repositoryCommit(req.Context(), sha)
		if err != nil {
			writeError(out, http.StatusInternalServerError, err.Error())
			return
		}
		comparison.Commits = append(comparison.Commits, commit)
	}
	if comparison.Files, err = s.diffFiles(req.Context(), mergeBase, head); err != nil {
		writeError(out, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(out, http.StatusOK, comparison)
}

// Verified reports whether GitHub signed a commit, as it does for commits a bot creates through the API.
func (s *Server) Verified(sha string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.signed[sha]
}

func compareStatus(ahead, behind int) string {
	switch {
	case ahead > 0 && behind > 0:
		return "diverged"
	case ahead > 0:
		return "ahead"
	case behind > 0:
		return "behind"
	default:
		return "identical"
	}
}

// revision resolves a branch, tag or commit to a commit, or the default branch when name is empty.
// It answers 404 and returns false when there's no such commit.
func (s *Server) revision(out http.ResponseWriter, req *http.Request, name string) (string, bool) {
	if name == "" {
		branch, err := s.defaultBranch(req.Context())
		if err != nil {
			writeError(out, http.StatusInternalServerError, err.Error())
			return "", false
		}
		name = branch
	}
	sha, ok, err := s.object(req.Context(), name+"^{commit}")
	if err != nil {
		writeError(out, http.StatusInternalServerError, err.Error())
		return "", false
	}
	if !ok {
		writeError(out, http.StatusNotFound, "No commit found for the ref "+name)
		return "", false
	}
	return sha, true
}

// writeRef answers with a reference to object, saying whether it is a commit or a tag object.
func (s *Server) writeRef(ctx context.Context, out http.ResponseWriter, status int, ref, object string) {
	kind, _, err := s.objectType(ctx, object)
	if err != nil {
		writeError(out, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(out, status, &github.Reference{
		Ref:    new(ref),
		Object: &github.GitObject{Type: new(kind), SHA: new(object)},
	})
}

// object returns the object rev names, unpeeled, and false if there is no such object.
// rev-parse takes a full object ID on trust, so the object's existence is checked too.
func (s *Server) object(ctx context.Context, rev string) (sha string, ok bool, err error) {
	out, err := git(ctx, s.origin, nil, "rev-parse", quiet, "--verify", rev)
	if _, missing := errors.AsType[*exec.ExitError](err); missing {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if _, err := git(ctx, s.origin, nil, "cat-file", "-e", out); err != nil {
		return "", false, nil //nolint:nilerr // a missing object is an answer, not an error
	}
	return out, true, nil
}

// objectType returns the type of the object rev names, and false if there is no such object.
func (s *Server) objectType(ctx context.Context, rev string) (kind string, ok bool, err error) {
	if _, ok, err = s.object(ctx, rev); err != nil || !ok {
		return "", false, err
	}
	kind, err = git(ctx, s.origin, nil, "cat-file", "-t", rev)
	return kind, err == nil, err
}

// tagObject reads an annotated tag, and returns false if sha isn't one.
func (s *Server) tagObject(ctx context.Context, sha string) (*github.Tag, bool, error) {
	if kind, ok, err := s.objectType(ctx, sha); err != nil || !ok || kind != typeTag {
		return nil, false, err
	}
	text, err := git(ctx, s.origin, nil, "cat-file", typeTag, sha)
	if err != nil {
		return nil, false, err
	}
	header, message, _ := strings.Cut(text, "\n\n")
	tag := &github.Tag{
		SHA: new(sha), Message: new(message + "\n"), Object: &github.GitObject{},
		Verification: &github.SignatureVerification{Verified: new(false), Reason: new("unsigned")},
	}
	for line := range strings.Lines(header) {
		key, value, _ := strings.Cut(strings.TrimSpace(line), " ")
		switch key {
		case "object":
			tag.Object.SHA = new(value)
		case "type":
			tag.Object.Type = new(value)
		case "tag":
			tag.Tag = new(value)
		case "tagger":
			tag.Tagger = parseSignature(value)
		}
	}
	return tag, true, nil
}

// commitObject reads a commit as the git database API shows it, and returns false if sha isn't one.
func (s *Server) commitObject(ctx context.Context, sha string) (*github.Commit, bool, error) {
	if kind, ok, err := s.objectType(ctx, sha); err != nil || !ok || kind != typeCommit {
		return nil, false, err
	}
	format := strings.Join([]string{"%H", "%T", "%P", "%an", "%ae", "%aI", "%cn", "%ce", "%cI", "%B"}, fieldSep)
	text, err := git(ctx, s.origin, nil, "log", "-1", "--format="+format, sha)
	if err != nil {
		return nil, false, err
	}
	fields := strings.SplitN(text, fieldSep, 10)
	if len(fields) != 10 {
		return nil, false, fmt.Errorf("%w: unexpected git log output %q", ErrUnexpected, text)
	}
	commit := &github.Commit{
		SHA:       new(fields[0]),
		Tree:      &github.Tree{SHA: new(fields[1])},
		Author:    author(fields[3], fields[4], fields[5]),
		Committer: author(fields[6], fields[7], fields[8]),
		Message:   new(strings.TrimRight(fields[9], "\n")),
		HTMLURL:   new(s.htmlURL() + "/commit/" + fields[0]),
	}
	for parent := range strings.FieldsSeq(fields[2]) {
		commit.Parents = append(commit.Parents, &github.Commit{SHA: new(parent)})
	}
	s.mu.Lock()
	verified := s.signed[sha]
	s.mu.Unlock()
	commit.Verification = &github.SignatureVerification{Verified: new(verified), Reason: new("unsigned")}
	if verified {
		commit.Verification.Reason = new("valid")
	}
	return commit, true, nil
}

// repositoryCommit reads a commit as the commits API shows it, without its files.
func (s *Server) repositoryCommit(ctx context.Context, sha string) (*github.RepositoryCommit, error) {
	commit, ok, err := s.commitObject(ctx, sha)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("%w: %s isn't a commit", ErrUnexpected, sha)
	}
	parents := commit.Parents
	commit.Parents = nil
	return &github.RepositoryCommit{SHA: commit.SHA, Commit: commit, Parents: parents, HTMLURL: commit.HTMLURL}, nil
}

// diffFiles lists the files that differ between two commits, as GitHub shows them. An empty from is the empty tree.
func (s *Server) diffFiles(ctx context.Context, from, to string) ([]*github.CommitFile, error) {
	args := []string{"diff-tree", "-r", "-M", "-z", "--no-commit-id", "--name-status"}
	if from == "" {
		args = append(args, "--root", to)
	} else {
		args = append(args, from, to)
	}
	out, err := gitBytes(ctx, s.origin, args...)
	if err != nil {
		return nil, err
	}
	statuses := map[byte]string{
		'A': "added", 'M': "modified", 'D': "removed", 'R': "renamed", 'C': "copied", 'T': "changed",
	}
	// Each change is a status and a path, with the old path before the new for renames and copies.
	fields := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
	var files []*github.CommitFile
	for len(fields) >= 2 {
		status := fields[0][0]
		file := &github.CommitFile{Status: new(statuses[status])}
		fields = fields[1:]
		if status == 'R' || status == 'C' {
			file.PreviousFilename = new(fields[0])
			fields = fields[1:]
		}
		file.Filename = new(fields[0])
		fields = fields[1:]
		files = append(files, file)
	}
	return files, nil
}

// commitTreeArgs returns the git commit-tree arguments that commit tree on parents,
// or the problem GitHub reports when they aren't a tree and commits.
func (s *Server) commitTreeArgs(ctx context.Context, tree string, parents []string) (args []string, problem string) {
	if kind, ok, err := s.objectType(ctx, tree); err != nil || !ok || kind != typeTree {
		return nil, "Tree SHA does not exist"
	}
	args = []string{"commit-tree", tree}
	for _, parent := range parents {
		if kind, ok, err := s.objectType(ctx, parent); err != nil || !ok || kind != typeCommit {
			return nil, "Parent SHA does not exist or is not a commit object"
		}
		args = append(args, "-p", parent)
	}
	return args, ""
}

// commitIdentity returns the environment that gives a new commit its author and committer:
// the token's bot unless the request names someone, and GitHub as committer of the commits it signs.
func commitIdentity(body *newCommit, signed bool) []string {
	author := identity(body.Author, botName, botEmail)
	committer := identity(body.Committer, author.GetName(), author.GetEmail())
	if signed {
		committer = identity(nil, githubName, githubEmail)
	}
	return []string{
		"GIT_AUTHOR_NAME=" + author.GetName(), "GIT_AUTHOR_EMAIL=" + author.GetEmail(),
		"GIT_AUTHOR_DATE=" + author.GetDate().Format(time.RFC3339),
		"GIT_COMMITTER_NAME=" + committer.GetName(), "GIT_COMMITTER_EMAIL=" + committer.GetEmail(),
		"GIT_COMMITTER_DATE=" + committer.GetDate().Format(time.RFC3339),
	}
}

// identity returns who, or the named identity when who is nil, dated now when it has no date.
func identity(who *github.CommitAuthor, name, email string) *github.CommitAuthor {
	if who == nil {
		who = &github.CommitAuthor{Name: new(name), Email: new(email)}
	}
	if who.Date == nil {
		who.Date = &github.Timestamp{Time: time.Now().UTC().Truncate(time.Second)}
	}
	return who
}

// signature formats an identity as git writes it in objects: "Name <email> seconds +0000".
func signature(who *github.CommitAuthor) string {
	return fmt.Sprintf("%s <%s> %d +0000", who.GetName(), who.GetEmail(), who.GetDate().Unix())
}

// parseSignature reads an identity from git's "Name <email> seconds zone".
func parseSignature(text string) *github.CommitAuthor {
	name, rest, _ := strings.Cut(text, " <")
	email, rest, _ := strings.Cut(rest, "> ")
	seconds, _, _ := strings.Cut(rest, " ")
	unix, _ := strconv.ParseInt(seconds, 10, 64)
	return &github.CommitAuthor{
		Name: new(name), Email: new(email), Date: &github.Timestamp{Time: time.Unix(unix, 0).UTC()},
	}
}

func author(name, email, date string) *github.CommitAuthor {
	when, _ := time.Parse(time.RFC3339, date)
	return &github.CommitAuthor{Name: new(name), Email: new(email), Date: &github.Timestamp{Time: when.UTC()}}
}

// wrapBase64 encodes content as GitHub does: base64 in lines of 60 characters.
func wrapBase64(content []byte) string {
	encoded := base64.StdEncoding.EncodeToString(content)
	var out strings.Builder
	for len(encoded) > base64Line {
		out.WriteString(encoded[:base64Line] + "\n")
		encoded = encoded[base64Line:]
	}
	out.WriteString(encoded)
	return out.String()
}

// gitInput runs a git command with input on stdin.
func gitInput(ctx context.Context, dir string, env []string, input string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // the fake's own git commands
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = strings.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, stderr.String())
	}
	return strings.TrimSpace(string(out)), nil
}

// gitBytes runs a git command, returning its output untrimmed.
func gitBytes(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // the fake's own git commands
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}
