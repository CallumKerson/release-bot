// Package planner decides which packages to release, and at which versions, from their commit histories.
// It is pure: callers collect the history and apply the plan.
package planner

import (
	"fmt"
	"strings"
	"time"

	"github.com/CallumKerson/release-bot/internal/config"
	"github.com/CallumKerson/release-bot/internal/conventional"
	"github.com/CallumKerson/release-bot/internal/manifest"
	"github.com/CallumKerson/release-bot/internal/version"
)

// releaseAsFooter sets a package's next version explicitly, overriding the computed one.
const releaseAsFooter = "Release-As"

// defaultBumps is how far each conventional commit type moves a version unless the config overrides it.
// Breaking changes are always major.
var defaultBumps = map[string]version.Bump{
	"feat":   version.Minor,
	"fix":    version.Patch,
	"perf":   version.Patch,
	"revert": version.Patch,
}

// Commit is a commit as the planner sees it.
type Commit struct {
	SHA     string
	Message string
	// Files are the paths the commit changed, relative to the repository root.
	Files []string
}

// Input is everything the planner needs.
type Input struct {
	Config   *config.Config
	Manifest manifest.Manifest
	// History holds each released package's commits since its last release, newest first.
	History map[string][]Commit
	Now     time.Time
}

// Entry is a commit that counts toward a package.
type Entry struct {
	SHA string `json:"sha"`
	// Summary is the first line of the commit message.
	Summary string `json:"summary"`
	// Commit is the parsed message, or nil when it isn't a conventional commit.
	Commit    *conventional.Commit `json:"commit,omitempty"`
	Bump      version.Bump         `json:"bump"`
	ReleaseAs string               `json:"release_as,omitempty"`
	Reasons   []Reason             `json:"reasons"`
}

// Inherited reports whether the entry only counts because of a dependency or an also glob.
func (e *Entry) Inherited() bool {
	for _, reason := range e.Reasons {
		if reason.Kind == ByPath || reason.Kind == ByEmptyCommit {
			return false
		}
	}
	return true
}

// PackagePlan is the plan for one released package.
type PackagePlan struct {
	Name      string       `json:"name"`
	Current   string       `json:"current,omitempty"`
	Next      string       `json:"next,omitempty"`
	Bump      version.Bump `json:"bump"`
	Tag       string       `json:"tag,omitempty"`
	Changelog string       `json:"changelog"`
	// Entries are the releasable commits, newest first.
	Entries []Entry `json:"entries,omitempty"`
	// Ignored are relevant commits that don't trigger a release, such as docs or non-conventional commits.
	Ignored []Entry `json:"ignored,omitempty"`
}

// Releasing reports whether the package gets a new version.
func (p *PackagePlan) Releasing() bool {
	return p.Next != ""
}

// Plan is the planner's decision for every released package, sorted by name.
type Plan struct {
	Packages []PackagePlan `json:"packages"`
}

// Releases returns the packages that get a new version.
func (p *Plan) Releases() []*PackagePlan {
	var releases []*PackagePlan
	for i := range p.Packages {
		if p.Packages[i].Releasing() {
			releases = append(releases, &p.Packages[i])
		}
	}
	return releases
}

// Build plans the next release of every released package.
func Build(in *Input) (Plan, error) {
	var plan Plan
	for _, pkg := range in.Config.Released() {
		pkgPlan, err := planPackage(in, pkg)
		if err != nil {
			return Plan{}, fmt.Errorf("planning %s: %w", pkg.Name, err)
		}
		plan.Packages = append(plan.Packages, pkgPlan)
	}
	return plan, nil
}

func planPackage(input *Input, pkg *config.Package) (PackagePlan, error) {
	scheme, err := pkg.VersionScheme()
	if err != nil {
		return PackagePlan{}, err
	}
	plan := PackagePlan{Name: pkg.Name, Current: input.Manifest[pkg.Name], Changelog: pkg.Changelog}
	if plan.Current != "" {
		if err := scheme.Validate(plan.Current); err != nil {
			return PackagePlan{}, fmt.Errorf("manifest version: %w", err)
		}
	}

	pkgs := closure(input.Config, pkg)
	history := input.History[pkg.Name]
	for i := range history {
		reasons := relevance(input.Config, pkgs, &history[i])
		if len(reasons) == 0 {
			continue
		}
		entry := newEntry(&history[i], reasons, input.Config.Bumps)
		if entry.Bump == version.None {
			plan.Ignored = append(plan.Ignored, entry)
			continue
		}
		plan.Entries = append(plan.Entries, entry)
		plan.Bump = max(plan.Bump, entry.Bump)
	}
	if plan.Bump == version.None {
		return plan, nil
	}

	plan.Next, err = nextVersion(scheme, &plan, input.Now)
	if err != nil {
		return PackagePlan{}, err
	}
	plan.Tag = pkg.TagFor(plan.Next)
	return plan, nil
}

func nextVersion(scheme version.Scheme, plan *PackagePlan, now time.Time) (string, error) {
	var next string
	var err error
	switch releaseAs := latestReleaseAs(plan.Entries); {
	case releaseAs != "":
		next, err = releaseAs, scheme.Validate(releaseAs)
		if err != nil {
			err = fmt.Errorf("%s footer: %w", releaseAsFooter, err)
		}
	case plan.Current == "":
		next, err = scheme.Initial(now)
	default:
		next, err = scheme.Next(plan.Current, plan.Bump, now)
	}
	if err != nil || plan.Current == "" {
		return next, err
	}
	order, err := scheme.Compare(next, plan.Current)
	if err != nil {
		return "", err
	}
	if order <= 0 {
		return "", fmt.Errorf("%w: next version %s must be after the current version %s",
			version.ErrInvalid, next, plan.Current)
	}
	return next, nil
}

// latestReleaseAs returns the Release-As footer of the newest entry that has one.
func latestReleaseAs(entries []Entry) string {
	for i := range entries {
		if entries[i].ReleaseAs != "" {
			return entries[i].ReleaseAs
		}
	}
	return ""
}

func newEntry(commit *Commit, reasons []Reason, overrides map[string]version.Bump) Entry {
	summary, _, _ := strings.Cut(strings.TrimSpace(commit.Message), "\n")
	entry := Entry{SHA: commit.SHA, Summary: summary, Reasons: reasons}
	parsed, ok := conventional.Parse(commit.Message)
	if !ok {
		return entry
	}
	entry.Commit = &parsed
	entry.Bump = classify(&parsed, overrides)
	if releaseAs, found := parsed.Footer(releaseAsFooter); found && releaseAs != "" {
		entry.ReleaseAs = releaseAs
		entry.Bump = max(entry.Bump, version.Patch)
	}
	return entry
}

func classify(commit *conventional.Commit, overrides map[string]version.Bump) version.Bump {
	if commit.Breaking {
		return version.Major
	}
	if bump, ok := overrides[commit.Type]; ok {
		return bump
	}
	return defaultBumps[commit.Type]
}
