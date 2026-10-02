// Package git reads history from, and writes tags and branches to, a local repository through the git CLI.
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/CallumKerson/release-bot/internal/vcs"
)

const (
	recordSep = "\x1e"
	fieldSep  = "\x1f"

	// Identity used for release commits when the repository has none configured, as on a fresh CI runner.
	fallbackName  = "release-bot"
	fallbackEmail = "release-bot@localhost"
)

var (
	// ErrCheckedOut is returned when asked to rewrite the branch that is checked out.
	ErrCheckedOut = errors.New("branch is checked out")
	// ErrNoCommits is returned for a repository whose HEAD has no commits yet.
	ErrNoCommits = errors.New("the repository has no commits")

	errLogOutput = errors.New("unexpected git log output")
)

// Repo is a local git repository.
type Repo struct {
	root string
	// identity is the environment that gives commits and tags an author.
	identity []string
}

// Open opens the repository containing dir.
func Open(ctx context.Context, dir string) (*Repo, error) {
	out, err := run(ctx, dir, nil, nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("%s is not in a git repository: %w", dir, err)
	}
	repo := &Repo{root: out}
	repo.identity = repo.fallbackIdentity(ctx)
	return repo, nil
}

// Root is the repository's top-level directory.
func (r *Repo) Root() string {
	return r.root
}

// Head returns the commit HEAD points to.
func (r *Repo) Head(ctx context.Context) (string, error) {
	sha, ok, err := r.resolve(ctx, "HEAD^{commit}")
	if err != nil {
		return "", err
	}
	if !ok {
		return "", ErrNoCommits
	}
	return sha, nil
}

// TagCommit returns the commit a tag points to, and false if there is no such tag.
func (r *Repo) TagCommit(ctx context.Context, tag string) (commit string, ok bool, err error) {
	return r.resolve(ctx, "refs/tags/"+tag+"^{commit}")
}

// Log returns the non-merge commits reachable from head but not from base, newest first, with the files each changed.
// An empty base means the whole history of head.
//
// Merge commits are skipped because the commits they bring in are listed themselves, with conventional messages.
// Topological order never lists a commit before its children, even when their timestamps tie,
// and keeps each merged branch's commits together.
// Renames are listed as a deletion and an addition, so a move between packages counts for both.
func (r *Repo) Log(ctx context.Context, base, head string) ([]vcs.Commit, error) {
	revs := head
	if base != "" {
		revs = base + ".." + head
	}
	out, err := r.git(ctx, nil, nil, "-c", "core.quotePath=false", "log", "--topo-order", "--no-merges", "--no-renames",
		"--name-only", "--format="+recordSep+"%H"+fieldSep+"%B"+fieldSep, revs, "--")
	if err != nil {
		return nil, err
	}
	var commits []vcs.Commit
	for record := range strings.SplitSeq(out, recordSep) {
		if strings.TrimSpace(record) == "" {
			continue
		}
		fields := strings.SplitN(record, fieldSep, 3)
		if len(fields) != 3 {
			return nil, fmt.Errorf("%w: %q", errLogOutput, record)
		}
		commit := vcs.Commit{SHA: fields[0], Message: strings.TrimSpace(fields[1])}
		for file := range strings.SplitSeq(fields[2], "\n") {
			if file = strings.TrimSpace(file); file != "" {
				commit.Files = append(commit.Files, file)
			}
		}
		commits = append(commits, commit)
	}
	return commits, nil
}

// ReadFile returns the contents of path at rev, and false if rev or path doesn't exist.
func (r *Repo) ReadFile(ctx context.Context, rev, path string) (content []byte, ok bool, err error) {
	object := rev + ":" + path
	if _, ok, err = r.resolve(ctx, object); err != nil || !ok {
		return nil, false, err
	}
	if content, err = r.gitRaw(ctx, nil, nil, "cat-file", "blob", object); err != nil {
		return nil, false, err
	}
	return content, true, nil
}

// FileHistory returns the commits reachable from rev that changed path, newest first.
func (r *Repo) FileHistory(ctx context.Context, rev, path string) ([]string, error) {
	out, err := r.git(ctx, nil, nil, "log", "--format=%H", rev, "--", path)
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

// CreateTag creates an annotated tag on commit.
func (r *Repo) CreateTag(ctx context.Context, tag, commit, message string) error {
	_, err := r.git(ctx, nil, r.identity, "tag", "--annotate", "--message", message, tag, commit)
	return err
}

// WriteBranch points branch at a new commit on top of parent that changes files, and returns that commit.
// It works on a temporary index, so the working tree, the real index and the checkout are never touched.
// When branch already holds exactly these changes on parent it is left alone, and changed is false.
func (r *Repo) WriteBranch(ctx context.Context, branch, parent string, files map[string][]byte, message string) (
	commit string, changed bool, err error,
) {
	ref := "refs/heads/" + branch
	if current, _ := r.git(ctx, nil, nil, "symbolic-ref", "--quiet", "HEAD"); current == ref {
		return "", false, fmt.Errorf("%w: refusing to rewrite %s, switch branch first", ErrCheckedOut, branch)
	}

	tree, err := r.writeTree(ctx, parent, files)
	if err != nil {
		return "", false, err
	}
	if existing, ok := r.unchanged(ctx, ref, parent, tree); ok {
		return existing, false, nil
	}

	commit, err = r.git(ctx, strings.NewReader(message), r.identity, "commit-tree", tree, "-p", parent, "-F", "-")
	if err != nil {
		return "", false, err
	}
	if _, err := r.git(ctx, nil, nil, "update-ref", "-m", "release-bot: rebuild "+branch, ref, commit); err != nil {
		return "", false, err
	}
	return commit, true, nil
}

// writeTree writes the tree of parent with files replaced, using a throwaway index.
func (r *Repo) writeTree(ctx context.Context, parent string, files map[string][]byte) (string, error) {
	dir, err := os.MkdirTemp("", "release-bot-index-")
	if err != nil {
		return "", fmt.Errorf("creating temporary index: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	env := []string{"GIT_INDEX_FILE=" + filepath.Join(dir, "index")}

	if _, err := r.git(ctx, nil, env, "read-tree", parent); err != nil {
		return "", err
	}
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	for _, path := range paths {
		blob, err := r.git(ctx, bytes.NewReader(files[path]), nil, "hash-object", "-w", "--stdin")
		if err != nil {
			return "", err
		}
		if _, err := r.git(ctx, nil, env, "update-index", "--add", "--cacheinfo", "100644,"+blob+","+path); err != nil {
			return "", err
		}
	}
	return r.git(ctx, nil, env, "write-tree")
}

// unchanged returns the commit ref points to when it already has tree on top of parent.
func (r *Repo) unchanged(ctx context.Context, ref, parent, tree string) (string, bool) {
	existing, ok, err := r.resolve(ctx, ref+"^{commit}")
	if err != nil || !ok {
		return "", false
	}
	existingTree, _, _ := r.resolve(ctx, existing+"^{tree}")
	existingParent, _, _ := r.resolve(ctx, existing+"^")
	return existing, existingTree == tree && existingParent == parent
}

// fallbackIdentity returns environment that gives commits and tags an author when git has none configured.
func (r *Repo) fallbackIdentity(ctx context.Context) []string {
	var env []string
	if name, _ := r.git(ctx, nil, nil, "config", "user.name"); name == "" {
		env = append(env, "GIT_AUTHOR_NAME="+fallbackName, "GIT_COMMITTER_NAME="+fallbackName)
	}
	if email, _ := r.git(ctx, nil, nil, "config", "user.email"); email == "" {
		env = append(env, "GIT_AUTHOR_EMAIL="+fallbackEmail, "GIT_COMMITTER_EMAIL="+fallbackEmail)
	}
	return env
}

// resolve returns the object rev names, and false if it doesn't exist.
func (r *Repo) resolve(ctx context.Context, rev string) (object string, ok bool, err error) {
	out, err := r.git(ctx, nil, nil, "rev-parse", "--quiet", "--verify", rev)
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 && out == "" {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return out, true, nil
}

func (r *Repo) git(ctx context.Context, stdin io.Reader, env []string, args ...string) (string, error) {
	return run(ctx, r.root, stdin, env, args...)
}

func (r *Repo) gitRaw(ctx context.Context, stdin io.Reader, env []string, args ...string) ([]byte, error) {
	return runRaw(ctx, r.root, stdin, env, args...)
}

func run(ctx context.Context, dir string, stdin io.Reader, env []string, args ...string) (string, error) {
	out, err := runRaw(ctx, dir, stdin, env, args...)
	return strings.TrimSpace(string(out)), err
}

func runRaw(ctx context.Context, dir string, stdin io.Reader, env []string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Stdin = stdin
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		return stdout.Bytes(), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, detail)
	}
	return stdout.Bytes(), nil
}
