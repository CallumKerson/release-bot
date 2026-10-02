// Package changelog renders release notes as markdown and adds them to CHANGELOG.md files.
package changelog

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/CallumKerson/release-bot/internal/planner"
)

const (
	title         = "# Changelog"
	shortSHA      = 7
	breakingTitle = "⚠ BREAKING CHANGES"
	otherTitle    = "Miscellaneous"
)

type section struct{ commitType, title string }

// sections lists the conventional commit types with their own heading, in the order they are rendered.
// Releasable commits of any other type are listed under otherTitle.
var sections = []section{
	{"feat", "Features"},
	{"fix", "Bug Fixes"},
	{"perf", "Performance Improvements"},
	{"revert", "Reverts"},
	{"deps", "Dependencies"},
}

// Render renders the release notes of one package release, dated by date.
func Render(release *planner.PackagePlan, date time.Time) string {
	var out strings.Builder
	fmt.Fprintf(&out, "## %s (%s)\n", release.Next, date.Format(time.DateOnly))

	var breaking []string
	grouped := map[string][]string{}
	for i := range release.Releasable {
		entry := &release.Releasable[i]
		if entry.Commit == nil {
			continue
		}
		if entry.Commit.Breaking {
			breaking = append(breaking, item(entry, breakingText(entry)))
		}
		heading := otherTitle
		if i := slices.IndexFunc(sections, func(s section) bool { return s.commitType == entry.Commit.Type }); i >= 0 {
			heading = sections[i].title
		}
		grouped[heading] = append(grouped[heading], item(entry, entry.Commit.Subject))
	}

	writeSection(&out, breakingTitle, breaking)
	for _, section := range sections {
		writeSection(&out, section.title, grouped[section.title])
	}
	writeSection(&out, otherTitle, grouped[otherTitle])
	return out.String()
}

func writeSection(out *strings.Builder, heading string, items []string) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(out, "\n### %s\n\n", heading)
	for _, line := range items {
		out.WriteString(line + "\n")
	}
}

func breakingText(entry *planner.Entry) string {
	if entry.Commit.BreakingNote != "" {
		return strings.Join(strings.Fields(entry.Commit.BreakingNote), " ")
	}
	return entry.Commit.Subject
}

func item(entry *planner.Entry, text string) string {
	var line strings.Builder
	line.WriteString("- ")
	if entry.Commit.Scope != "" {
		line.WriteString("**" + entry.Commit.Scope + ":** ")
	}
	line.WriteString(text)
	fmt.Fprintf(&line, " (%s)", entry.SHA[:min(len(entry.SHA), shortSHA)])
	if entry.Inherited() {
		line.WriteString(" (via " + strings.Join(vias(entry), ", ") + ")")
	}
	return line.String()
}

// vias names what an inherited entry came through: dependencies by name, globs as code.
func vias(entry *planner.Entry) []string {
	var out []string
	for _, reason := range entry.Reasons {
		via := reason.Via
		if reason.Kind == planner.ByAlso {
			via = "`" + via + "`"
		}
		if !slices.Contains(out, via) {
			out = append(out, via)
		}
	}
	return out
}

// Prepend adds a rendered release to the top of an existing changelog, below its title.
// An empty existing changelog gets a title.
func Prepend(existing, release string) string {
	rest := strings.TrimSpace(existing)
	if after, ok := strings.CutPrefix(rest, title); ok && (after == "" || after[0] == '\n') {
		rest = strings.TrimSpace(after)
	}
	out := title + "\n\n" + strings.TrimSpace(release) + "\n"
	if rest != "" {
		out += "\n" + rest + "\n"
	}
	return out
}
