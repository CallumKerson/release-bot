package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/CallumKerson/release-bot/internal/planner"
	"github.com/CallumKerson/release-bot/internal/release"
)

const shortSHA = 7

func short(sha string) string {
	return sha[:min(len(sha), shortSHA)]
}

// printPlan explains every package's next release and the commits behind it.
func printPlan(out io.Writer, result *release.Result) {
	if len(result.Tags) > 0 {
		fmt.Fprintln(out, "Merged releases to tag:")
		for _, tag := range result.Tags {
			fmt.Fprintf(out, "  %s on %s\n", tag.Name, short(tag.Commit))
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
			fmt.Fprintf(out, "  ! %s (%s)\n", entry.Summary, short(entry.SHA))
		}
	}

	fmt.Fprintln(out)
	if result.Branch == nil {
		fmt.Fprintln(out, "Nothing to release.")
		return
	}
	fmt.Fprintf(out, "Release branch %s would change:\n", result.Branch.Name)
	for _, path := range result.Branch.Paths {
		fmt.Fprintf(out, "  %s\n", path)
	}
}

func printPackage(out io.Writer, pkg *planner.PackagePlan) {
	current := pkg.Current
	if current == "" {
		current = "unreleased"
	}
	if pkg.Releasing() {
		fmt.Fprintf(out, "%s: %s -> %s (%s)\n", pkg.Name, current, pkg.Next, pkg.Bump)
	} else {
		fmt.Fprintf(out, "%s: %s, nothing to release\n", pkg.Name, current)
	}
	for i := range pkg.Entries {
		entry := &pkg.Entries[i]
		fmt.Fprintf(out, "  + %s (%s)\n", entry.Summary, short(entry.SHA))
		for _, reason := range entry.Reasons {
			fmt.Fprintf(out, "      %s\n", explain(pkg.Name, reason))
		}
	}
	for i := range pkg.Ignored {
		entry := &pkg.Ignored[i]
		fmt.Fprintf(out, "  - %s (%s), %s\n", entry.Summary, short(entry.SHA), ignoredBecause(entry))
	}
}

// printRun summarises what a run did, or with --dry-run, would do.
func printRun(out io.Writer, result *release.Result) {
	if result.Nothing() {
		fmt.Fprintln(out, "Nothing to release.")
		return
	}
	for _, tag := range result.Tags {
		if result.Applied {
			fmt.Fprintf(out, "Tagged %s on %s\n", tag.Name, short(tag.Commit))
		} else {
			fmt.Fprintf(out, "Would tag %s on %s\n", tag.Name, short(tag.Commit))
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
	case !result.Applied:
		fmt.Fprintf(out, "Would update %s: %s\n", result.Branch.Name, summary)
	case result.BranchChanged:
		fmt.Fprintf(out, "Updated %s on %s: %s\n", result.Branch.Name, short(result.BranchCommit), summary)
	default:
		fmt.Fprintf(out, "%s is already up to date: %s\n", result.Branch.Name, summary)
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
