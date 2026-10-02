package main

import (
	"errors"
	"fmt"
	"strings"
)

// errHistory is wrapped by every mistake in a feature's git history.
var errHistory = errors.New("can't read the history")

// historyCommit is one commit of the git history written in a feature file:
//
//	A  chore: first release
//	   changes   apps/app-a/main.go, libs/lib-1/lib.go
//	   manifest  app-a 1.2.0, app-b 2026.09.0
//	   tags      app-a-v1.2.0, app-b-v2026.09.0
//
//	B  feat(lib-1): add retries
//	   changes   libs/lib-1/retry.go
//	   footer    BREAKING CHANGE: retries are on by default
//
// An unindented line starts a commit: a label for the commit, then its message.
// Indented lines describe the commit, one keyword each:
//
//	changes   files the commit creates or edits, separated by commas
//	deletes   files the commit deletes
//	manifest  the versions the commit writes to the manifest, as "package version" pairs
//	tags      tags to create on the commit
//	body      a line of the commit message body
//	footer    a footer line at the end of the commit message
//
// Blank lines and lines starting with # are ignored.
type historyCommit struct {
	label    string
	subject  string
	body     []string
	footers  []string
	changes  []string
	deletes  []string
	manifest map[string]string
	tags     []string
}

func (c *historyCommit) message() string {
	paragraphs := []string{c.subject}
	if len(c.body) > 0 {
		paragraphs = append(paragraphs, strings.Join(c.body, "\n"))
	}
	if len(c.footers) > 0 {
		paragraphs = append(paragraphs, strings.Join(c.footers, "\n"))
	}
	return strings.Join(paragraphs, "\n\n")
}

func parseHistory(text string) ([]historyCommit, error) {
	var commits []historyCommit
	for i, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		fail := func(err error) error { return fmt.Errorf("history line %d %q: %w", i+1, trimmed, err) }

		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			label, subject, _ := strings.Cut(trimmed, " ")
			if subject = strings.TrimSpace(subject); subject == "" {
				return nil, fail(fmt.Errorf("%w: a commit needs a label and a message", errHistory))
			}
			commits = append(commits, historyCommit{label: label, subject: subject})
			continue
		}

		if len(commits) == 0 {
			return nil, fail(fmt.Errorf("%w: it describes a commit before any commit has started", errHistory))
		}
		if err := describe(&commits[len(commits)-1], trimmed); err != nil {
			return nil, fail(err)
		}
	}
	return commits, nil
}

func describe(commit *historyCommit, line string) error {
	keyword, value, _ := strings.Cut(line, " ")
	value = strings.TrimSpace(value)
	switch keyword {
	case "changes":
		commit.changes = append(commit.changes, list(value)...)
	case "deletes":
		commit.deletes = append(commit.deletes, list(value)...)
	case "tags":
		commit.tags = append(commit.tags, list(value)...)
	case "body":
		commit.body = append(commit.body, value)
	case "footer":
		commit.footers = append(commit.footers, value)
	case "manifest":
		commit.manifest = map[string]string{}
		for _, pair := range list(value) {
			name, version, ok := strings.Cut(pair, " ")
			if !ok {
				return fmt.Errorf("%w: manifest entry %q should be a package and a version", errHistory, pair)
			}
			commit.manifest[name] = strings.TrimSpace(version)
		}
	default:
		return fmt.Errorf("%w: unknown keyword %q, want changes, deletes, manifest, tags, body or footer",
			errHistory, keyword)
	}
	return nil
}

func list(value string) []string {
	var items []string
	for item := range strings.SplitSeq(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}
	return items
}
