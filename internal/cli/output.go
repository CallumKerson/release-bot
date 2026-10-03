package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/CallumKerson/release-bot/internal/planner"
	"github.com/CallumKerson/release-bot/internal/release"
	"github.com/CallumKerson/release-bot/internal/vcs"
)

// printPlan explains every package's next release and the commits behind it.
func printPlan(out io.Writer, result *release.Result) {
	if len(result.Untagged) > 0 {
		fmt.Fprintln(out, "Merged releases to tag:")
		for _, tag := range result.Untagged {
			fmt.Fprintf(out, "  %s on %s\n", tag.Name, vcs.Short(tag.ReleaseCommit))
		}
		fmt.Fprintln(out)
	}

	for i := range result.Plan.Packages {
		printPackage(out, &result.Plan.Packages[i])
	}

	if len(result.Plan.Unplaced) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Empty commits that count toward no package, as their scope doesn't name a released package:")
		for i := range result.Plan.Unplaced {
			entry := &result.Plan.Unplaced[i]
			fmt.Fprintf(out, "  ! %s (%s)\n", entry.Summary, vcs.Short(entry.SHA))
		}
	}

	fmt.Fprintln(out)
	if result.Branch == nil {
		fmt.Fprintln(out, "Nothing to release.")
		return
	}
	fmt.Fprintf(out, "Release branch %s would change:\n", result.Branch.Name)
	for _, path := range result.Branch.Paths() {
		fmt.Fprintf(out, "  %s\n", path)
	}
}

func printPackage(out io.Writer, pkg *planner.PackagePlan) {
	current := pkg.CurrentOrUnreleased()
	if pkg.Releasing() {
		fmt.Fprintf(out, "%s: %s -> %s (%s)\n", pkg.Name, current, pkg.Next, pkg.Bump)
	} else {
		fmt.Fprintf(out, "%s: %s, nothing to release\n", pkg.Name, current)
	}
	for i := range pkg.Releasable {
		entry := &pkg.Releasable[i]
		fmt.Fprintf(out, "  + %s (%s)\n", entry.Summary, vcs.Short(entry.SHA))
		for _, reason := range entry.Reasons {
			fmt.Fprintf(out, "      %s\n", explain(pkg.Name, reason))
		}
	}
	for i := range pkg.Ignored {
		entry := &pkg.Ignored[i]
		fmt.Fprintf(out, "  - %s (%s), %s\n", entry.Summary, vcs.Short(entry.SHA), ignoredBecause(entry))
	}
}

// printRun summarises what a run did, or for a dry run with no outcome, would do.
func printRun(out io.Writer, run *runOutput) {
	printApplied(out, run.Result, run.Outcome)
	if run.github {
		printPublished(out, run.Result, run.Published)
	}
}

// printApplied summarises what a run did to the local repository, or would do.
func printApplied(out io.Writer, result *release.Result, outcome *release.Outcome) {
	if result.Nothing() {
		fmt.Fprintln(out, "Nothing to release.")
		return
	}
	for _, tag := range result.Untagged {
		if outcome != nil {
			fmt.Fprintf(out, "Tagged %s on %s\n", tag.Name, vcs.Short(tag.ReleaseCommit))
		} else {
			fmt.Fprintf(out, "Would tag %s on %s\n", tag.Name, vcs.Short(tag.ReleaseCommit))
		}
	}
	if result.Branch == nil {
		return
	}

	releases := make([]string, 0, len(result.Plan.Packages))
	for _, pkg := range result.Plan.Releases() {
		releases = append(releases, pkg.Name+" "+pkg.Next)
	}
	summary := strings.Join(releases, ", ")
	switch {
	case outcome == nil:
		fmt.Fprintf(out, "Would update %s: %s\n", result.Branch.Name, summary)
	case outcome.BranchChanged:
		fmt.Fprintf(out, "Updated %s on %s: %s\n", result.Branch.Name, vcs.Short(outcome.BranchCommit), summary)
	default:
		fmt.Fprintf(out, "%s is already up to date: %s\n", result.Branch.Name, summary)
	}
}

// printPublished summarises what a run did on GitHub, or for a dry run with nothing published, would do.
// Releases that were already published go unmentioned.
func printPublished(out io.Writer, result *release.Result, published *release.Published) {
	if published == nil {
		for _, tag := range result.Untagged {
			fmt.Fprintf(out, "Would publish release %s\n", tag.Name)
		}
		if result.Branch != nil {
			fmt.Fprintf(out, "Would push %s and open or update its pull request\n", result.Branch.Name)
		}
		return
	}
	for _, rel := range published.Releases {
		if rel.Created {
			fmt.Fprintf(out, "Published release %s: %s\n", rel.Tag, rel.URL)
		}
	}
	if published.BranchPushed {
		fmt.Fprintf(out, "Pushed %s\n", result.Branch.Name)
	}
	if pull := published.PullRequest; pull != nil {
		switch pull.Action {
		case release.PullRequestOpened:
			fmt.Fprintf(out, "Opened pull request #%d: %s\n", pull.Number, pull.URL)
		case release.PullRequestUpdated:
			fmt.Fprintf(out, "Updated pull request #%d: %s\n", pull.Number, pull.URL)
		case release.PullRequestUnchanged:
			fmt.Fprintf(out, "Pull request #%d is already up to date: %s\n", pull.Number, pull.URL)
		}
	}
}

func explain(pkg string, reason planner.Reason) string {
	switch reason.Kind {
	case planner.ByDependency:
		return fmt.Sprintf("%s is in %s, which %s depends on", reason.File, reason.Via, pkg)
	case planner.ByAlso:
		return fmt.Sprintf("%s matches %s", reason.File, reason.Via)
	case planner.ByPath:
		return fmt.Sprintf("%s is in %s", reason.File, pkg)
	case planner.ByEmptyCommit:
		return "an empty commit counts for the whole repository"
	case planner.ByScope:
		return "an empty commit counts for the package its scope names"
	default:
		return reason.File
	}
}

func ignoredBecause(entry *planner.Entry) string {
	if entry.Commit == nil {
		return "not a conventional commit"
	}
	return entry.Commit.Type + " commits aren't released"
}
