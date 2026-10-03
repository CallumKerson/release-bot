// Package releasetest holds an in-memory release.Repo, and the contract tests every release.Repo must pass.
package releasetest

import (
	"bytes"
	"context"
	"crypto/sha1" //nolint:gosec // commit IDs, as in git, not security
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/CallumKerson/release-bot/internal/vcs"
)

// checkedOut is the branch the fake has checked out, as a new git repository would.
const checkedOut = "main"

var (
	// ErrUnknownRevision is returned for a revision that names no commit.
	ErrUnknownRevision = errors.New("unknown revision")
	// ErrTagExists is returned when creating a tag that already exists.
	ErrTagExists = errors.New("tag already exists")
	// ErrCheckedOut is returned when asked to rewrite the checked out branch.
	ErrCheckedOut = errors.New("branch is checked out")
)

// Commit is a commit to build a repository from.
type Commit struct {
	Message string
	// Files are the contents of the files the commit creates or edits, by path.
	Files map[string]string
	Tags  []string
}

type fakeCommit struct {
	sha     string
	parent  string
	message string
	tree    map[string][]byte
	changed []string
}

// parents returns the commit's parent, or none for the first commit.
func (c *fakeCommit) parents() []string {
	if c.parent == "" {
		return nil
	}
	return []string{c.parent}
}

// Fake is an in-memory repository with one line of history on main, as a code host such as GitHub holds it.
type Fake struct {
	commits  map[string]*fakeCommit
	branches map[string]string
	tags     map[string]string
}

// NewFake builds a Fake whose main branch has history, oldest commit first.
func NewFake(history ...Commit) *Fake {
	fake := &Fake{
		commits:  map[string]*fakeCommit{},
		branches: map[string]string{},
		tags:     map[string]string{},
	}
	for _, commit := range history {
		files := map[string][]byte{}
		for path, content := range commit.Files {
			files[path] = []byte(content)
		}
		sha := fake.commit(fake.branches[checkedOut], files, commit.Message)
		fake.branches[checkedOut] = sha
		for _, tag := range commit.Tags {
			fake.tags[tag] = sha
		}
	}
	return fake
}

// Branch returns the commit a branch points to, and false if there is no such branch.
func (f *Fake) Branch(name string) (string, bool) {
	sha, ok := f.branches[name]
	return sha, ok
}

// Tags returns the fake's tags and the commits they point to.
func (f *Fake) Tags() map[string]string {
	return maps.Clone(f.tags)
}

func (f *Fake) commit(parent string, files map[string][]byte, message string) string {
	tree := map[string][]byte{}
	if parent != "" {
		tree = maps.Clone(f.commits[parent].tree)
	}
	var changed []string
	for path, content := range files {
		if old, ok := tree[path]; !ok || !bytes.Equal(old, content) {
			changed = append(changed, path)
		}
		tree[path] = content
	}
	slices.Sort(changed)
	sum := sha1.Sum(fmt.Appendf(nil, "%s\x00%s\x00%d", parent, message, len(f.commits))) //nolint:gosec // see import
	sha := hex.EncodeToString(sum[:])
	f.commits[sha] = &fakeCommit{sha: sha, parent: parent, message: message, tree: tree, changed: changed}
	return sha
}

// resolve finds the commit rev names: a commit ID, a branch or a tag.
func (f *Fake) resolve(rev string) (*fakeCommit, bool) {
	sha := rev
	if branch, ok := f.branches[rev]; ok {
		sha = branch
	} else if tag, ok := f.tags[rev]; ok {
		sha = tag
	}
	commit, found := f.commits[sha]
	return commit, found
}

// ancestry returns rev and its ancestors, newest first.
func (f *Fake) ancestry(rev string) ([]*fakeCommit, error) {
	commit, ok := f.resolve(rev)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownRevision, rev)
	}
	var out []*fakeCommit
	for ; commit != nil; commit = f.commits[commit.parent] {
		out = append(out, commit)
	}
	return out, nil
}

// Head returns the commit main points to.
func (f *Fake) Head(context.Context) (string, error) {
	sha, ok := f.branches[checkedOut]
	if !ok {
		return "", fmt.Errorf("%w: %s has no commits", ErrUnknownRevision, checkedOut)
	}
	return sha, nil
}

// TagCommit returns the commit a tag points to, and false if there is no such tag.
func (f *Fake) TagCommit(_ context.Context, tag string) (commit string, ok bool, err error) {
	commit, ok = f.tags[tag]
	return commit, ok, nil
}

// Log returns the commits reachable from head but not from base, newest first. An empty base means all of them.
func (f *Fake) Log(_ context.Context, base, head string) ([]vcs.Commit, error) {
	commits, err := f.ancestry(head)
	if err != nil {
		return nil, err
	}
	var excluded []*fakeCommit
	if base != "" {
		if excluded, err = f.ancestry(base); err != nil {
			return nil, err
		}
	}
	var out []vcs.Commit
	for _, commit := range commits {
		if !slices.Contains(excluded, commit) {
			out = append(out, vcs.Commit{
				SHA: commit.sha, Message: commit.message, Parents: commit.parents(), Files: commit.changed,
			})
		}
	}
	return out, nil
}

// ReadFile returns the contents of path at rev, and false if rev or path doesn't exist.
func (f *Fake) ReadFile(_ context.Context, rev, path string) (content []byte, ok bool, err error) {
	commit, ok := f.resolve(rev)
	if !ok {
		return nil, false, nil
	}
	content, ok = commit.tree[path]
	return content, ok, nil
}

// FileHistory returns the commits reachable from rev that changed path, newest first, with their parents.
func (f *Fake) FileHistory(_ context.Context, rev, path string) ([]vcs.Commit, error) {
	commits, err := f.ancestry(rev)
	if err != nil {
		return nil, err
	}
	var out []vcs.Commit
	for _, commit := range commits {
		if slices.Contains(commit.changed, path) {
			out = append(out, vcs.Commit{SHA: commit.sha, Parents: commit.parents()})
		}
	}
	return out, nil
}

// CreateTag tags commit.
func (f *Fake) CreateTag(_ context.Context, tag, commit, _ string) error {
	if _, ok := f.tags[tag]; ok {
		return fmt.Errorf("%w: %s", ErrTagExists, tag)
	}
	target, ok := f.resolve(commit)
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownRevision, commit)
	}
	f.tags[tag] = target.sha
	return nil
}

// WriteBranch points branch at a new commit on top of parent that changes files,
// unless it already holds exactly these changes on parent.
func (f *Fake) WriteBranch(_ context.Context, branch, parent string, files map[string][]byte, message string) (
	commit string, changed bool, err error,
) {
	if branch == checkedOut {
		return "", false, fmt.Errorf("%w: %s", ErrCheckedOut, branch)
	}
	base, ok := f.resolve(parent)
	if !ok {
		return "", false, fmt.Errorf("%w: %s", ErrUnknownRevision, parent)
	}
	tree := maps.Clone(base.tree)
	maps.Copy(tree, files)
	if existing, ok := f.commits[f.branches[branch]]; ok && existing.parent == base.sha &&
		maps.EqualFunc(existing.tree, tree, bytes.Equal) {
		return existing.sha, false, nil
	}
	commit = f.commit(base.sha, files, message)
	f.branches[branch] = commit
	return commit, true, nil
}
