package conventional

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		message string
		want    Commit
	}{
		{
			name:    "type and subject",
			message: "fix: handle nil config",
			want:    Commit{Type: "fix", Subject: "handle nil config"},
		},
		{
			name:    "scope",
			message: "feat(lib-1): add retries\n",
			want:    Commit{Type: "feat", Scope: "lib-1", Subject: "add retries"},
		},
		{
			name:    "type is lower cased",
			message: "Feat: shout",
			want:    Commit{Type: "feat", Subject: "shout"},
		},
		{
			name:    "bang marks breaking",
			message: "refactor(api)!: drop v1 endpoints",
			want:    Commit{Type: "refactor", Scope: "api", Breaking: true, Subject: "drop v1 endpoints"},
		},
		{
			name:    "body without footers",
			message: "fix: x\n\nLonger explanation.\n\nAnd more: still body.",
			want:    Commit{Type: "fix", Subject: "x", Body: "Longer explanation.\n\nAnd more: still body."},
		},
		{
			name:    "breaking change footer",
			message: "feat: new config\n\nExplains it.\n\nBREAKING CHANGE: config moved\nto a new file\nRefs: #12",
			want: Commit{
				Type: "feat", Subject: "new config", Body: "Explains it.",
				Breaking: true, BreakingNote: "config moved\nto a new file",
				Footers: []Footer{
					{Token: "BREAKING CHANGE", Value: "config moved\nto a new file"},
					{Token: "Refs", Value: "#12"},
				},
			},
		},
		{
			name:    "hyphenated breaking change footer",
			message: "fix: y\n\nBREAKING-CHANGE: gone",
			want: Commit{
				Type: "fix", Subject: "y", Breaking: true, BreakingNote: "gone",
				Footers: []Footer{{Token: "BREAKING-CHANGE", Value: "gone"}},
			},
		},
		{
			name:    "hash footer",
			message: "chore: release\n\nRelease-As: 2.0.0\nCloses #4",
			want: Commit{
				Type: "chore", Subject: "release",
				Footers: []Footer{{Token: "Release-As", Value: "2.0.0"}, {Token: "Closes", Value: "4"}},
			},
		},
		{
			name:    "crlf line endings",
			message: "fix: windows\r\n\r\nRefs: #1\r\n",
			want:    Commit{Type: "fix", Subject: "windows", Footers: []Footer{{Token: "Refs", Value: "#1"}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Parse(tt.message)
			assert.True(t, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseRejectsNonConventional(t *testing.T) {
	for _, message := range []string{
		"",
		"Initial commit",
		"Merge pull request #1 from x/y",
		"fix:no space",
		"fix: ",
		"feat(scope: unclosed",
		"wip!!: nope",
	} {
		_, ok := Parse(message)
		assert.False(t, ok, message)
	}
}

func TestFooterLookup(t *testing.T) {
	commit, ok := Parse("chore: x\n\nrelease-as: 1.0.0\nRelease-As: 2.0.0")
	assert.True(t, ok)
	value, found := commit.Footer("RELEASE-AS")
	assert.True(t, found)
	assert.Equal(t, "2.0.0", value)
	_, found = commit.Footer("Refs")
	assert.False(t, found)
}

func TestHeader(t *testing.T) {
	commit, _ := Parse("feat(api)!: drop v1\n\nbody")
	assert.Equal(t, "feat(api)!: drop v1", commit.Header())
	commit, _ = Parse("fix: plain")
	assert.Equal(t, "fix: plain", commit.Header())
}
