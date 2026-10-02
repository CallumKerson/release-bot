// Package conventional parses Conventional Commits 1.0.0 messages.
package conventional

import (
	"regexp"
	"slices"
	"strings"
)

// Footer is a git trailer style line at the end of a commit message, such as "Release-As: 2.0.0".
type Footer struct {
	Token string `json:"token"`
	Value string `json:"value"`
}

// Commit is a parsed conventional commit message.
type Commit struct {
	Type     string `json:"type"`
	Scope    string `json:"scope,omitempty"`
	Subject  string `json:"subject"`
	Breaking bool   `json:"breaking,omitempty"`
	// BreakingNote is the text of a BREAKING CHANGE footer, if there is one.
	BreakingNote string   `json:"breaking_note,omitempty"`
	Body         string   `json:"body,omitempty"`
	Footers      []Footer `json:"footers,omitempty"`
}

var (
	headerPattern = regexp.MustCompile(`^([A-Za-z]+)(?:\(([^()\r\n]*)\))?(!)?: (\S.*)$`)
	footerPattern = regexp.MustCompile(`^(BREAKING CHANGE|BREAKING-CHANGE|[A-Za-z][A-Za-z0-9-]*)(?:: | #)(.*)$`)
)

// Parse parses message as a conventional commit.
// It reports false when the header isn't in conventional form.
func Parse(message string) (Commit, bool) {
	message = strings.ReplaceAll(strings.TrimSpace(message), "\r\n", "\n")
	header, rest, _ := strings.Cut(message, "\n")
	match := headerPattern.FindStringSubmatch(strings.TrimSpace(header))
	if match == nil {
		return Commit{}, false
	}
	commit := Commit{
		Type:     strings.ToLower(match[1]),
		Scope:    strings.TrimSpace(match[2]),
		Breaking: match[3] == "!",
		Subject:  strings.TrimSpace(match[4]),
	}

	body, footers := splitFooters(strings.TrimSpace(rest))
	commit.Body = body
	commit.Footers = footers
	for _, footer := range footers {
		if footer.Token == "BREAKING CHANGE" || footer.Token == "BREAKING-CHANGE" {
			commit.Breaking = true
			commit.BreakingNote = footer.Value
		}
	}
	return commit, true
}

// Footer returns the value of the last footer with the given token, matched case-insensitively.
func (c *Commit) Footer(token string) (string, bool) {
	for _, v := range slices.Backward(c.Footers) {
		if strings.EqualFold(v.Token, token) {
			return v.Value, true
		}
	}
	return "", false
}

// Header renders the commit's first line in conventional form.
func (c *Commit) Header() string {
	var header strings.Builder
	header.WriteString(c.Type)
	if c.Scope != "" {
		header.WriteString("(" + c.Scope + ")")
	}
	if c.Breaking {
		header.WriteString("!")
	}
	header.WriteString(": " + c.Subject)
	return header.String()
}

// splitFooters separates the trailing footer paragraph from the body.
// A paragraph only counts as footers when its first line is a footer;
// later lines that aren't footers continue the previous footer's value.
func splitFooters(text string) (string, []Footer) {
	if text == "" {
		return "", nil
	}
	paragraphs := strings.Split(text, "\n\n")
	last := paragraphs[len(paragraphs)-1]
	lines := strings.Split(last, "\n")
	if !footerPattern.MatchString(lines[0]) {
		return text, nil
	}

	var footers []Footer
	for _, line := range lines {
		if match := footerPattern.FindStringSubmatch(line); match != nil {
			footers = append(footers, Footer{Token: match[1], Value: strings.TrimSpace(match[2])})
			continue
		}
		previous := &footers[len(footers)-1]
		previous.Value = strings.TrimSpace(previous.Value + "\n" + line)
	}
	body := strings.TrimSpace(strings.Join(paragraphs[:len(paragraphs)-1], "\n\n"))
	return body, footers
}
