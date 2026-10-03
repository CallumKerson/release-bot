package github

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	gogithub "github.com/google/go-github/v92/github"

	"github.com/CallumKerson/release-bot/internal/vcs"
)

const (
	// pageSize is the most items the REST API lists per page.
	pageSize = 100
	// fileMode is the mode of the files a release commit writes: regular, not executable.
	fileMode = "100644"
)

// ErrTargetBranch is returned when asked to rewrite the branch releases are made from.
var ErrTargetBranch = errors.New("refusing to rewrite the target branch")

// Head returns the commit the target branch points to. It is read once, so a run sees one commit throughout.
func (h *Host) Head(ctx context.Context) (string, error) {
	if h.head != "" {
		return h.head, nil
	}
	branch, err := h.targetBranch(ctx)
	if err != nil {
		return "", err
	}
	ref, _, err := h.client.Git.GetRef(ctx, h.owner, h.name, "heads/"+branch)
	if err != nil {
		return "", fmt.Errorf("reading branch %s: %w", branch, err)
	}
	h.head = ref.GetObject().GetSHA()
	return h.head, nil
}

// TagCommit returns the commit a tag points to, and false if there is no such tag.
func (h *Host) TagCommit(ctx context.Context, tag string) (commit string, ok bool, err error) {
	ref, _, err := h.client.Git.GetRef(ctx, h.owner, h.name, "tags/"+tag)
	if notFound(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("reading tag %s: %w", tag, err)
	}
	object := ref.GetObject()
	for object.GetType() == "tag" {
		annotated, _, err := h.client.Git.GetTag(ctx, h.owner, h.name, object.GetSHA())
		if err != nil {
			return "", false, fmt.Errorf("reading tag %s: %w", tag, err)
		}
		object = annotated.GetObject()
	}
	return object.GetSHA(), true, nil
}

// Log returns the non-merge commits reachable from head but not from base, newest first, with the files each changed.
// An empty base means the whole history of head.
//
// It orders them as git log --topo-order does, and lists a rename as a deletion and an addition,
// so it agrees with the git adapter.
func (h *Host) Log(ctx context.Context, base, head string) ([]vcs.Commit, error) {
	listed, err := h.listCommits(ctx, base, head)
	if err != nil {
		return nil, err
	}
	var commits []vcs.Commit
	for _, commit := range topoOrder(listed) {
		if len(commit.Parents) > 1 {
			continue
		}
		files, err := h.commitFiles(ctx, commit.GetSHA())
		if err != nil {
			return nil, err
		}
		commits = append(commits, vcs.Commit{
			SHA:     commit.GetSHA(),
			Message: strings.TrimSpace(commit.GetCommit().GetMessage()),
			Parents: parentSHAs(commit.Parents),
			Files:   files,
		})
	}
	return commits, nil
}

// ReadFile returns the contents of path at rev, and false if rev or path doesn't exist.
func (h *Host) ReadFile(ctx context.Context, rev, path string) (content []byte, ok bool, err error) {
	file, _, _, err := h.client.Repositories.GetContents(ctx, h.owner, h.name, path,
		&gogithub.RepositoryContentGetOptions{Ref: rev})
	if notFound(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("reading %s at %s: %w", path, vcs.Short(rev), err)
	}
	if file == nil {
		return nil, false, fmt.Errorf("reading %s at %s: %w", path, vcs.Short(rev), gogithub.ErrContentsDirectory)
	}
	// The contents API leaves out files over 1 MB, which the blob API still serves.
	if file.GetEncoding() == "none" {
		if content, _, err = h.client.Git.GetBlobRaw(ctx, h.owner, h.name, file.GetSHA()); err != nil {
			return nil, false, fmt.Errorf("reading %s at %s: %w", path, vcs.Short(rev), err)
		}
		return content, true, nil
	}
	text, err := file.GetContent()
	if err != nil {
		return nil, false, fmt.Errorf("reading %s at %s: %w", path, vcs.Short(rev), err)
	}
	return []byte(text), true, nil
}

// FileHistory returns the commits reachable from rev that changed path, newest first, with their parents.
func (h *Host) FileHistory(ctx context.Context, rev, path string) ([]vcs.Commit, error) {
	opts := &gogithub.CommitsListOptions{SHA: rev, Path: path, PerPage: pageSize}
	var history []vcs.Commit
	for {
		page, resp, err := h.client.Repositories.ListCommits(ctx, h.owner, h.name, opts)
		if err != nil {
			return nil, fmt.Errorf("listing the commits that changed %s: %w", path, err)
		}
		for _, commit := range page {
			history = append(history, vcs.Commit{SHA: commit.GetSHA(), Parents: parentSHAs(commit.Parents)})
		}
		if resp.NextPage == 0 {
			return history, nil
		}
		opts.Page = resp.NextPage
	}
}

// CreateTag creates an annotated tag on commit, tagged by the token's identity.
func (h *Host) CreateTag(ctx context.Context, tag, commit, message string) error {
	object, _, err := h.client.Git.CreateTag(ctx, h.owner, h.name, gogithub.CreateTag{
		Tag: tag, Message: message, Object: commit, Type: "commit",
	})
	if err != nil {
		return fmt.Errorf("creating tag %s: %w", tag, err)
	}
	if _, _, err := h.client.Git.CreateRef(ctx, h.owner, h.name, gogithub.CreateRef{
		Ref: "refs/tags/" + tag, SHA: object.GetSHA(),
	}); err != nil {
		return fmt.Errorf("creating tag %s: %w", tag, err)
	}
	return nil
}

// WriteBranch points branch at a new commit on top of parent that changes files, and returns that commit.
// When branch already holds exactly these changes on parent it is left alone, and changed is false.
//
// The commit has no author or committer, so GitHub makes it as the token's identity and, for a bot such as
// GitHub Actions or a GitHub App, signs it. The files must be text, as the API takes their content as a string.
func (h *Host) WriteBranch(ctx context.Context, branch, parent string, files map[string][]byte, message string) (
	commit string, changed bool, err error,
) {
	target, err := h.targetBranch(ctx)
	if err != nil {
		return "", false, err
	}
	if branch == target {
		return "", false, fmt.Errorf("%w: %s is the branch releases are made from", ErrTargetBranch, branch)
	}
	base, _, err := h.client.Git.GetCommit(ctx, h.owner, h.name, parent)
	if err != nil {
		return "", false, fmt.Errorf("reading commit %s: %w", vcs.Short(parent), err)
	}
	entries := make([]*gogithub.TreeEntry, 0, len(files))
	for _, path := range slices.Sorted(maps.Keys(files)) {
		entries = append(entries, &gogithub.TreeEntry{
			Path: new(path), Mode: new(fileMode), Type: new("blob"), Content: new(string(files[path])),
		})
	}
	tree, _, err := h.client.Git.CreateTree(ctx, h.owner, h.name, base.GetTree().GetSHA(), entries)
	if err != nil {
		return "", false, fmt.Errorf("writing the tree of %s: %w", branch, err)
	}

	existing, err := h.branchCommit(ctx, branch)
	if err != nil {
		return "", false, err
	}
	if existing != nil && existing.GetTree().GetSHA() == tree.GetSHA() &&
		slices.Equal(parentSHAs(existing.Parents), []string{parent}) {
		return existing.GetSHA(), false, nil
	}

	created, _, err := h.client.Git.CreateCommit(ctx, h.owner, h.name, gogithub.Commit{
		Message: &message, Tree: &gogithub.Tree{SHA: tree.SHA}, Parents: []*gogithub.Commit{{SHA: &parent}},
	}, nil)
	if err != nil {
		return "", false, fmt.Errorf("committing to %s: %w", branch, err)
	}
	if existing == nil {
		_, _, err = h.client.Git.CreateRef(ctx, h.owner, h.name, gogithub.CreateRef{
			Ref: "refs/heads/" + branch, SHA: created.GetSHA(),
		})
	} else {
		_, _, err = h.client.Git.UpdateRef(ctx, h.owner, h.name, "heads/"+branch, gogithub.UpdateRef{
			SHA: created.GetSHA(), Force: new(true),
		})
	}
	if err != nil {
		return "", false, fmt.Errorf("pointing %s at %s: %w", branch, vcs.Short(created.GetSHA()), err)
	}
	return created.GetSHA(), true, nil
}

// branchCommit returns the commit branch points to, or nil if there is no such branch.
func (h *Host) branchCommit(ctx context.Context, branch string) (*gogithub.Commit, error) {
	ref, _, err := h.client.Git.GetRef(ctx, h.owner, h.name, "heads/"+branch)
	if notFound(err) {
		return nil, nil //nolint:nilnil // no branch is a valid answer, not an error
	}
	if err != nil {
		return nil, fmt.Errorf("reading branch %s: %w", branch, err)
	}
	commit, _, err := h.client.Git.GetCommit(ctx, h.owner, h.name, ref.GetObject().GetSHA())
	if err != nil {
		return nil, fmt.Errorf("reading branch %s: %w", branch, err)
	}
	return commit, nil
}

// listCommits returns the commits reachable from head but not from base, or all of head's history without a base,
// newest first by the API's order.
//
// The comparison leaves out what is reachable from the merge base of base and head,
// which is the same as leaving out what is reachable from base.
func (h *Host) listCommits(ctx context.Context, base, head string) ([]*gogithub.RepositoryCommit, error) {
	opts := gogithub.ListOptions{PerPage: pageSize}
	var commits []*gogithub.RepositoryCommit
	for {
		var page []*gogithub.RepositoryCommit
		var resp *gogithub.Response
		var err error
		if base == "" {
			page, resp, err = h.client.Repositories.ListCommits(ctx, h.owner, h.name,
				&gogithub.CommitsListOptions{SHA: head, ListOptions: opts})
		} else {
			var comparison *gogithub.CommitsComparison
			comparison, resp, err = h.client.Repositories.CompareCommits(ctx, h.owner, h.name, base, head, &opts)
			if comparison != nil {
				page = comparison.Commits
				slices.Reverse(page) // comparisons list the oldest first
			}
		}
		if err != nil {
			return nil, fmt.Errorf("listing commits: %w", err)
		}
		if base == "" {
			commits = append(commits, page...)
		} else {
			commits = append(page, commits...)
		}
		if resp.NextPage == 0 {
			return commits, nil
		}
		opts.Page = resp.NextPage
	}
}

// commitFiles returns the paths a commit changed, with both the old and new path of a rename.
func (h *Host) commitFiles(ctx context.Context, sha string) ([]string, error) {
	opts := &gogithub.ListOptions{}
	var files []string
	for {
		commit, resp, err := h.client.Repositories.GetCommit(ctx, h.owner, h.name, sha, opts)
		if err != nil {
			return nil, fmt.Errorf("reading commit %s: %w", vcs.Short(sha), err)
		}
		for _, file := range commit.Files {
			if previous := file.GetPreviousFilename(); previous != "" {
				files = append(files, previous)
			}
			files = append(files, file.GetFilename())
		}
		if resp.NextPage == 0 {
			return files, nil
		}
		opts.Page = resp.NextPage
	}
}

// topoOrder orders commits, given newest first, so that none comes before its children, as git log --topo-order
// does: from the newest, it follows each line of history to its end before going back to another,
// taking a merge's last parent first, so a merged branch's commits come together, before the merge base.
func topoOrder(commits []*gogithub.RepositoryCommit) []*gogithub.RepositoryCommit {
	byID := make(map[string]*gogithub.RepositoryCommit, len(commits))
	for _, commit := range commits {
		byID[commit.GetSHA()] = commit
	}
	children := map[string]int{}
	for _, commit := range commits {
		for _, parent := range parentSHAs(commit.Parents) {
			if _, listed := byID[parent]; listed {
				children[parent]++
			}
		}
	}
	var ready []*gogithub.RepositoryCommit
	for _, commit := range slices.Backward(commits) {
		if children[commit.GetSHA()] == 0 {
			ready = append(ready, commit)
		}
	}
	ordered := make([]*gogithub.RepositoryCommit, 0, len(commits))
	for len(ready) > 0 {
		commit := ready[len(ready)-1]
		ready = ready[:len(ready)-1]
		ordered = append(ordered, commit)
		for _, parent := range parentSHAs(commit.Parents) {
			if _, listed := byID[parent]; !listed {
				continue
			}
			children[parent]--
			if children[parent] == 0 {
				ready = append(ready, byID[parent])
			}
		}
	}
	return ordered
}

func parentSHAs(parents []*gogithub.Commit) []string {
	if len(parents) == 0 {
		return nil
	}
	shas := make([]string, 0, len(parents))
	for _, parent := range parents {
		shas = append(shas, parent.GetSHA())
	}
	return shas
}
