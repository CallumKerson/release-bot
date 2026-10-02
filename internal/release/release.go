// Package release decides what a run does to a repository, and does it.
//
// A run either tags release commits that have been merged, rebuilds the release branch with the next versions,
// or does nothing. Prepare works out which, without writing anything, and Apply carries it out.
package release

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/CallumKerson/release-bot/internal/changelog"
	"github.com/CallumKerson/release-bot/internal/config"
	"github.com/CallumKerson/release-bot/internal/manifest"
	"github.com/CallumKerson/release-bot/internal/planner"
	"github.com/CallumKerson/release-bot/internal/vcs"
)

// ErrNoReleaseCommit is returned when the manifest has a version that no commit in its history set.
var ErrNoReleaseCommit = errors.New("no commit released this version")

// Repo is the version control a run reads from and writes to.
type Repo interface {
	Head(ctx context.Context) (string, error)
	TagCommit(ctx context.Context, tag string) (commit string, ok bool, err error)
	Log(ctx context.Context, base, head string) ([]vcs.Commit, error)
	ReadFile(ctx context.Context, rev, path string) (content []byte, ok bool, err error)
	FileHistory(ctx context.Context, rev, path string) ([]string, error)
	CreateTag(ctx context.Context, tag, commit, message string) error
	WriteBranch(
		ctx context.Context,
		branch, parent string,
		files map[string][]byte,
		message string,
	) (commit string, changed bool, err error)
}

// Tag is a release that has been merged but not yet tagged.
type Tag struct {
	Package       string `json:"package"`
	Version       string `json:"version"`
	Name          string `json:"name"`
	ReleaseCommit string `json:"release_commit"`
}

// Branch is the content of the release branch.
type Branch struct {
	Name    string
	Message string
	// Files are the new contents of the changed files, keyed by repository path.
	Files map[string][]byte
}

// Paths returns the paths of the changed files, sorted.
func (b *Branch) Paths() []string {
	return slices.Sorted(maps.Keys(b.Files))
}

// MarshalJSON lists the changed files by path, leaving out their contents.
func (b *Branch) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Name    string   `json:"name"`
		Message string   `json:"message"`
		Files   []string `json:"files"`
	}{b.Name, b.Message, b.Paths()})
}

// Result is what a run will do.
type Result struct {
	Head     string       `json:"head"`
	Untagged []Tag        `json:"untagged,omitempty"`
	Plan     planner.Plan `json:"plan"`
	// Branch is nil when nothing is ready to release.
	Branch *Branch `json:"branch,omitempty"`
}

// Nothing reports whether the run has nothing to do.
func (r *Result) Nothing() bool {
	return len(r.Untagged) == 0 && r.Branch == nil
}

// Outcome is what Apply did.
type Outcome struct {
	BranchCommit string `json:"branch_commit,omitempty"`
	// BranchChanged is false when the release branch was already up to date.
	BranchChanged bool `json:"branch_changed"`
}

// Prepare works out what a run does to the repository at HEAD, without changing anything.
func Prepare(ctx context.Context, repo Repo, cfg *config.Config, now time.Time) (*Result, error) {
	head, err := repo.Head(ctx)
	if err != nil {
		return nil, err
	}
	current, err := readManifest(ctx, repo, cfg, head)
	if err != nil {
		return nil, err
	}

	result := &Result{Head: head}
	bases := map[string]string{}
	var untagged []Tag
	for _, pkg := range cfg.Released() {
		ver, ok := current[pkg.Name]
		if !ok {
			continue
		}
		tag := Tag{Package: pkg.Name, Version: ver, Name: pkg.TagFor(ver)}
		commit, tagged, err := repo.TagCommit(ctx, tag.Name)
		if err != nil {
			return nil, err
		}
		if tagged {
			bases[pkg.Name] = commit
		} else {
			untagged = append(untagged, tag)
		}
	}
	if result.Untagged, err = releaseCommits(ctx, repo, cfg.Manifest, head, untagged); err != nil {
		return nil, err
	}
	for _, tag := range result.Untagged {
		bases[tag.Package] = tag.ReleaseCommit
	}

	history, err := collect(ctx, repo, cfg, bases, head)
	if err != nil {
		return nil, err
	}
	result.Plan, err = planner.Build(&planner.Input{Config: cfg, Manifest: current, History: history, Now: now})
	if err != nil {
		return nil, err
	}
	if result.Branch, err = buildBranch(ctx, repo, cfg, head, current, &result.Plan, now); err != nil {
		return nil, err
	}
	return result, nil
}

// Apply creates the tags and writes the release branch that Prepare decided on.
func Apply(ctx context.Context, repo Repo, result *Result) (*Outcome, error) {
	for _, tag := range result.Untagged {
		if err := repo.CreateTag(ctx, tag.Name, tag.ReleaseCommit, tag.Package+" "+tag.Version); err != nil {
			return nil, err
		}
	}
	outcome := &Outcome{}
	if result.Branch != nil {
		commit, changed, err := repo.WriteBranch(ctx, result.Branch.Name, result.Head, result.Branch.Files,
			result.Branch.Message)
		if err != nil {
			return nil, err
		}
		outcome.BranchCommit, outcome.BranchChanged = commit, changed
	}
	return outcome, nil
}

func readManifest(ctx context.Context, repo Repo, cfg *config.Config, rev string) (manifest.Manifest, error) {
	data, _, err := repo.ReadFile(ctx, rev, cfg.Manifest)
	if err != nil {
		return nil, err
	}
	versions, err := manifest.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s at %s: %w", cfg.Manifest, short(rev), err)
	}
	for name := range versions {
		if pkg := cfg.Package(name); pkg == nil || !pkg.Release {
			return nil, fmt.Errorf("%w: %s lists %q, which isn't a released package in the config",
				manifest.ErrInvalid, cfg.Manifest, name)
		}
	}
	return versions, nil
}

// collect returns each released package's commits since its base, sharing logs between packages with the same base.
func collect(ctx context.Context, repo Repo, cfg *config.Config, bases map[string]string, head string) (
	map[string][]vcs.Commit, error,
) {
	logs := map[string][]vcs.Commit{}
	history := map[string][]vcs.Commit{}
	for _, pkg := range cfg.Released() {
		base := bases[pkg.Name]
		commits, ok := logs[base]
		if !ok {
			var err error
			if commits, err = repo.Log(ctx, base, head); err != nil {
				return nil, err
			}
			logs[base] = commits
		}
		history[pkg.Name] = commits
	}
	return history, nil
}

// buildBranch renders the release branch for the plan, or returns nil when nothing is releasing.
func buildBranch(ctx context.Context, repo Repo, cfg *config.Config, head string, current manifest.Manifest,
	plan *planner.Plan, now time.Time,
) (*Branch, error) {
	releases := plan.Releases()
	if len(releases) == 0 {
		return nil, nil //nolint:nilnil // no branch is a valid outcome, not an error
	}

	files := map[string][]byte{}
	versions := map[string]string{}
	var summary, body []string
	for _, rel := range releases {
		existing, _, err := repo.ReadFile(ctx, head, rel.Changelog)
		if err != nil {
			return nil, err
		}
		files[rel.Changelog] = []byte(changelog.Prepend(string(existing), changelog.Render(rel, now)))
		versions[rel.Name] = rel.Next
		summary = append(summary, rel.Name+" "+rel.Next)
		from := rel.Current
		if from == "" {
			from = "unreleased"
		}
		body = append(body, fmt.Sprintf("- %s %s -> %s", rel.Name, from, rel.Next))
	}
	data, err := current.With(versions).Marshal()
	if err != nil {
		return nil, err
	}
	files[cfg.Manifest] = data

	return &Branch{
		Name:    cfg.Branch,
		Message: "chore(release): " + strings.Join(summary, ", ") + "\n\n" + strings.Join(body, "\n") + "\n",
		Files:   files,
	}, nil
}

// releaseCommits finds the commit that released each untagged version: the newest commit that set it in the manifest.
// It walks the manifest's history once, newest first, comparing each commit's manifest with its first parent's.
func releaseCommits(ctx context.Context, repo Repo, path, head string, untagged []Tag) ([]Tag, error) {
	if len(untagged) == 0 {
		return nil, nil
	}
	history, err := repo.FileHistory(ctx, head, path)
	if err != nil {
		return nil, err
	}
	found := make([]Tag, 0, len(untagged))
	remaining := slices.Clone(untagged)
	for _, commit := range history {
		after, err := manifestAt(ctx, repo, commit, path)
		if err != nil {
			return nil, err
		}
		before, err := manifestAt(ctx, repo, commit+"^", path)
		if err != nil {
			return nil, err
		}
		remaining = slices.DeleteFunc(remaining, func(tag Tag) bool {
			if after[tag.Package] != tag.Version || before[tag.Package] == tag.Version {
				return false
			}
			tag.ReleaseCommit = commit
			found = append(found, tag)
			return true
		})
		if len(remaining) == 0 {
			break
		}
	}
	if len(remaining) > 0 {
		return nil, fmt.Errorf("%w: %s %s in %s", ErrNoReleaseCommit, remaining[0].Package, remaining[0].Version, path)
	}
	slices.SortFunc(found, func(a, b Tag) int { return strings.Compare(a.Package, b.Package) })
	return found, nil
}

// manifestAt reads the manifest at rev. A missing or broken manifest is empty,
// since it can't be the one that released the current version.
func manifestAt(ctx context.Context, repo Repo, rev, path string) (manifest.Manifest, error) {
	data, _, err := repo.ReadFile(ctx, rev, path)
	if err != nil {
		return nil, err
	}
	versions, err := manifest.Parse(data)
	if err != nil {
		return manifest.Manifest{}, nil //nolint:nilerr // see above
	}
	return versions, nil
}

func short(sha string) string {
	return sha[:min(len(sha), 7)]
}
