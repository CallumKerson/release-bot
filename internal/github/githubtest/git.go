package githubtest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/go-github/v92/github"
)

// Flags of the fake's git commands.
const (
	quiet   = "--quiet"
	message = "--message"
)

// GitHub's identity on the commits it makes when merging.
const (
	githubName  = "GitHub"
	githubEmail = "noreply@github.com"
)

// How a pull request is merged, as on GitHub's merge button.
const (
	MergeCommit = "merge"
	Squash      = "squash"
	Rebase      = "rebase"
)

var (
	// ErrNotMergeable is returned when merging a pull request that is closed or doesn't merge cleanly.
	ErrNotMergeable = errors.New("pull request is not mergeable")
	// ErrUnknownMethod is returned for a merge method GitHub doesn't have.
	ErrUnknownMethod = errors.New("unknown merge method")
)

// MergePullRequest merges a pull request the way GitHub's merge button does, and returns the base branch's new commit.
//
//   - A merge commit is titled "Merge pull request #n from owner/branch", with the pull request's title as its body.
//   - A squash merge is one commit titled "<pull request title> (#n)", with the squashed commits' bodies as its body.
//   - A rebase replays each commit onto the base with GitHub as committer, so they always get new hashes.
func (s *Server) MergePullRequest(ctx context.Context, number int, method string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pull := s.pull(strconv.Itoa(number))
	if pull == nil || pull.GetState() != stateOpen {
		return "", fmt.Errorf("%w: #%d isn't open", ErrNotMergeable, number)
	}
	headSHA, _, err := s.ref(ctx, "refs/heads/"+pull.GetHead().GetRef())
	if err != nil {
		return "", err
	}

	dir, err := os.MkdirTemp("", "githubtest-merge-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	if _, err := git(ctx, "", nil, "clone", quiet, s.origin, dir); err != nil {
		return "", err
	}
	if err := s.merge(ctx, dir, pull, method); err != nil {
		return "", err
	}
	if _, err := git(
		ctx,
		dir,
		nil,
		"push",
		quiet,
		"origin",
		"merging:refs/heads/"+pull.GetBase().GetRef(),
	); err != nil {
		return "", err
	}
	merged, err := git(ctx, dir, nil, "rev-parse", "merging")
	if err != nil {
		return "", err
	}

	now := github.Timestamp{Time: time.Now().UTC()}
	pull.State, pull.Merged = new("closed"), new(true)
	pull.MergedAt, pull.ClosedAt, pull.UpdatedAt = &now, &now, &now
	pull.MergeCommitSHA = new(merged)
	pull.Head.SHA = new(headSHA)
	return merged, nil
}

// merge makes the branch "merging" in the clone at dir hold the base branch with the pull request merged into it.
func (s *Server) merge(ctx context.Context, dir string, pull *github.PullRequest, method string) error {
	head, base := "origin/"+pull.GetHead().GetRef(), "origin/"+pull.GetBase().GetRef()
	committer := []string{"GIT_COMMITTER_NAME=" + githubName, "GIT_COMMITTER_EMAIL=" + githubEmail}
	merger := append([]string{"GIT_AUTHOR_NAME=" + githubName, "GIT_AUTHOR_EMAIL=" + githubEmail}, committer...)

	start := base
	if method == Rebase {
		start = head
	}
	if _, err := git(ctx, dir, nil, "checkout", quiet, "-B", "merging", start); err != nil {
		return err
	}

	var steps [][]string
	var env []string
	switch method {
	case MergeCommit, Squash:
		env = merger
		flags := []string{"--no-ff", "--no-commit"}
		text := fmt.Sprintf("Merge pull request #%d from %s/%s\n\n%s",
			pull.GetNumber(), s.owner, pull.GetHead().GetRef(), pull.GetTitle())
		if method == Squash {
			bodies, err := git(ctx, dir, nil, "log", "--format=%b", base+".."+head)
			if err != nil {
				return err
			}
			flags = []string{"--squash"}
			text = fmt.Sprintf("%s (#%d)\n\n%s", pull.GetTitle(), pull.GetNumber(), bodies)
		}
		steps = [][]string{
			slices.Concat([]string{"merge", quiet}, flags, []string{head}),
			{"commit", quiet, "--allow-empty", message, text},
		}
	case Rebase:
		env = committer
		steps = [][]string{{"rebase", quiet, "--force-rebase", base}}
	default:
		return fmt.Errorf("%w: %q", ErrUnknownMethod, method)
	}
	for _, step := range steps {
		if _, err := git(ctx, dir, env, step...); err != nil {
			return fmt.Errorf("%w: #%d: %w", ErrNotMergeable, pull.GetNumber(), err)
		}
	}
	return nil
}

// defaultBranch is the branch the bare repository's HEAD names.
func (s *Server) defaultBranch(ctx context.Context) (string, error) {
	return git(ctx, s.origin, nil, "symbolic-ref", "--short", "HEAD")
}

// ref returns the commit a ref points to, and false if there is no such ref.
func (s *Server) ref(ctx context.Context, ref string) (commit string, ok bool, err error) {
	out, err := git(ctx, s.origin, nil, "rev-parse", quiet, "--verify", ref+"^{commit}")
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return "", false, nil
	}
	return out, err == nil, err
}

// ahead reports whether head has commits that base doesn't.
func (s *Server) ahead(ctx context.Context, base, head string) (bool, error) {
	count, err := git(ctx, s.origin, nil, "rev-list", "--count", base+".."+head)
	return count != "0", err
}

// lightweightTag tags the commit target names, a branch or a commit.
func (s *Server) lightweightTag(ctx context.Context, tag, target string) error {
	commit, err := git(ctx, s.origin, nil, "rev-parse", "--verify", target+"^{commit}")
	if err != nil {
		return err
	}
	_, err = git(ctx, s.origin, nil, "update-ref", "refs/tags/"+tag, commit, "")
	return err
}

func git(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // the fake's own git commands
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, exitErr.Stderr)
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
