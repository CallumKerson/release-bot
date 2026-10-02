package changelog

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CallumKerson/release-bot/internal/conventional"
	"github.com/CallumKerson/release-bot/internal/planner"
	"github.com/CallumKerson/release-bot/internal/version"
)

var update = flag.Bool("update", false, "rewrite golden files")

var date = time.Date(2026, time.October, 2, 0, 0, 0, 0, time.UTC)

func golden(t *testing.T, name, got string) {
	t.Helper()
	file := filepath.Join("testdata", name)
	if *update {
		require.NoError(t, os.WriteFile(file, []byte(got), 0o600))
	}
	want, err := os.ReadFile(file)
	require.NoError(t, err)
	assert.Equal(t, string(want), got)
}

func entry(sha, message string, reasons ...planner.Reason) planner.Entry {
	parsed, ok := conventional.Parse(message)
	result := planner.Entry{SHA: sha, Summary: message, Bump: version.Patch, Reasons: reasons}
	if ok {
		result.Commit = &parsed
	}
	if len(reasons) == 0 {
		result.Reasons = []planner.Reason{{Kind: planner.ByPath, File: "apps/app-a/x.go"}}
	}
	return result
}

func TestRender(t *testing.T) {
	release := planner.PackagePlan{
		Name: "app-a", Current: "1.2.0", Next: "2.0.0", Bump: version.Major,
		Releasable: []planner.Entry{
			entry("1111111aaaa", "feat(api)!: drop v1 endpoints"),
			entry("2222222bbbb", "feat: add retries\n\nBREAKING CHANGE: retry config\nmoved to [retry]"),
			entry("3333333cccc", "fix(lib-1): handle nil",
				planner.Reason{Kind: planner.ByDependency, Via: "lib-1", File: "libs/lib-1/a.go"}),
			entry("4444444dddd", "perf: cache lookups"),
			entry("5555555eeee", "revert: undo thing"),
			entry("6666666ffff", "deps: bump x to 1.2"),
			entry("7777777aaaa", "chore: go stable\n\nRelease-As: 2.0.0"),
			entry("8888888bbbb", "feat: new rpc",
				planner.Reason{Kind: planner.ByAlso, Via: "proto/**", File: "proto/a.proto"},
				planner.Reason{Kind: planner.ByDependency, Via: "lib-2", File: "libs/lib-2/a.go"}),
		},
	}
	golden(t, "render.md", Render(&release, date))
}

func TestRenderSingleSection(t *testing.T) {
	release := planner.PackagePlan{
		Name:       "tool",
		Next:       "0.1.1",
		Releasable: []planner.Entry{entry("abc", "fix: short sha")},
	}
	assert.Equal(t, "## 0.1.1 (2026-10-02)\n\n### Bug Fixes\n\n- short sha (abc)\n", Render(&release, date))
}

func TestPrepend(t *testing.T) {
	release := "## 1.1.0 (2026-10-02)\n\n### Features\n\n- new (abc1234)\n"
	tests := map[string]struct{ existing, want string }{
		"empty": {
			existing: "",
			want:     "# Changelog\n\n" + release,
		},
		"with title and history": {
			existing: "# Changelog\n\n## 1.0.0 (2026-01-01)\n\n### Bug Fixes\n\n- old (def5678)\n",
			want:     "# Changelog\n\n" + release + "\n## 1.0.0 (2026-01-01)\n\n### Bug Fixes\n\n- old (def5678)\n",
		},
		"history without title": {
			existing: "## 1.0.0\n\n- old\n\n\n",
			want:     "# Changelog\n\n" + release + "\n## 1.0.0\n\n- old\n",
		},
		"similar title is kept": {
			existing: "# Changelogs of note\n",
			want:     "# Changelog\n\n" + release + "\n# Changelogs of note\n",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tt.want, Prepend(tt.existing, release))
		})
	}
}
