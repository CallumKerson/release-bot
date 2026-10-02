package features

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseHistory(t *testing.T) {
	commits, err := parseHistory(`
# the first release
A  chore: first release
   changes   apps/app-a/main.go, libs/lib-1/lib.go
   manifest  app-a 1.2.0, app-b 2026.09.0
   tags      app-a-v1.2.0, app-b-v2026.09.0

B  feat(lib-1): add retries (#12)
   deletes  libs/lib-1/old.go
   body     Retries use exponential backoff.
   footer   BREAKING CHANGE: retries are on by default
   footer   Refs: #12
`)
	require.NoError(t, err)
	require.Len(t, commits, 2)

	assert.Equal(t, historyCommit{
		label:    "A",
		subject:  "chore: first release",
		changes:  []string{"apps/app-a/main.go", "libs/lib-1/lib.go"},
		manifest: map[string]string{"app-a": "1.2.0", "app-b": "2026.09.0"},
		tags:     []string{"app-a-v1.2.0", "app-b-v2026.09.0"},
	}, commits[0])
	assert.Equal(t, "chore: first release", commits[0].message())

	assert.Equal(t, []string{"libs/lib-1/old.go"}, commits[1].deletes)
	assert.Equal(t, "feat(lib-1): add retries (#12)\n\nRetries use exponential backoff.\n\n"+
		"BREAKING CHANGE: retries are on by default\nRefs: #12", commits[1].message())
}

func TestParseHistoryErrors(t *testing.T) {
	tests := map[string]string{
		"no message":               "A",
		"description before any":   "   changes a.go",
		"unknown keyword":          "A  fix: x\n   touches a.go",
		"manifest without version": "A  fix: x\n   manifest app-a",
	}
	for name, history := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := parseHistory(history)
			require.ErrorIs(t, err, errHistory)
		})
	}
}
