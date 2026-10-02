package git

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrTagConflict is returned when the remote already has a tag on a different commit.
var ErrTagConflict = errors.New("the remote has the tag on a different commit")

// Remote pushes tags and branches from a repository to one of its remotes.
type Remote struct {
	repo *Repo
	name string
}

// Remote returns the repository's remote called name, such as "origin".
func (r *Repo) Remote(name string) *Remote {
	return &Remote{repo: r, name: name}
}

// PushTags pushes the tags the remote doesn't have yet.
// A tag the remote already has on the same commit is left alone, and one on a different commit is an error.
func (r *Remote) PushTags(ctx context.Context, tags []string) error {
	remote, err := r.refs(ctx, "--tags")
	if err != nil {
		return err
	}
	var refspecs []string
	for _, tag := range tags {
		ref := "refs/tags/" + tag
		local, err := r.repo.git(ctx, nil, nil, "rev-parse", "--verify", ref+"^{commit}")
		if err != nil {
			return err
		}
		pushed, ok := remote[ref]
		switch {
		case !ok:
			refspecs = append(refspecs, ref+":"+ref)
		case pushed != local:
			return fmt.Errorf("%w: %s is on %s in %s, not %s", ErrTagConflict, tag, pushed, r.name, local)
		}
	}
	if len(refspecs) == 0 {
		return nil
	}
	_, err = r.repo.git(ctx, nil, nil, append([]string{"push", "--quiet", r.name}, refspecs...)...)
	return err
}

// PushBranch points the remote's branch at commit, unless it already points there.
// The push replaces the branch only if it still holds what it held a moment before, so a concurrent push isn't lost.
func (r *Remote) PushBranch(ctx context.Context, branch, commit string) (changed bool, err error) {
	ref := "refs/heads/" + branch
	remote, err := r.refs(ctx, "--heads")
	if err != nil {
		return false, err
	}
	current := remote[ref]
	if current == commit {
		return false, nil
	}
	_, err = r.repo.git(ctx, nil, nil,
		"push", "--quiet", "--force-with-lease="+ref+":"+current, r.name, commit+":"+ref)
	return err == nil, err
}

// refs lists the remote's refs of one kind, mapped to the commits they point to, with annotated tags peeled.
func (r *Remote) refs(ctx context.Context, kind string) (map[string]string, error) {
	out, err := r.repo.git(ctx, nil, nil, "ls-remote", kind, r.name)
	if err != nil {
		return nil, err
	}
	refs := map[string]string{}
	for line := range strings.Lines(out) {
		object, ref, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok {
			continue
		}
		if peeled, isPeeled := strings.CutSuffix(ref, "^{}"); isPeeled {
			refs[peeled] = object
		} else if _, seen := refs[ref]; !seen {
			refs[ref] = object
		}
	}
	return refs, nil
}
