package planner

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CallumKerson/release-bot/internal/config"
	"github.com/CallumKerson/release-bot/internal/manifest"
	"github.com/CallumKerson/release-bot/internal/version"
)

const monorepo = `
[bump]
deps = "patch"

[packages.app-a]
path = "apps/app-a"
depends-on = ["lib-1", "lib-2"]

[packages.app-b]
path = "apps/app-b"
depends-on = ["lib-1"]
scheme = "calver"

[packages.lib-1]
path = "libs/lib-1"
release = false
depends-on = ["lib-3"]

[packages.lib-2]
path = "libs/lib-2"
release = false
also = ["proto/**"]
exclude = ["libs/lib-2/docs/**"]

[packages.lib-3]
path = "libs/lib-3"
release = false
`

var now = time.Date(2026, time.October, 2, 9, 0, 0, 0, time.UTC)

func mustConfig(t *testing.T, toml string) *config.Config {
	t.Helper()
	cfg, err := config.Parse([]byte(toml))
	require.NoError(t, err)
	return &cfg
}

// sameHistory gives every released package the same commits, as if all were last released at the same commit.
func sameHistory(cfg *config.Config, commits ...Commit) map[string][]Commit {
	history := map[string][]Commit{}
	for _, pkg := range cfg.Released() {
		history[pkg.Name] = commits
	}
	return history
}

func build(t *testing.T, cfg *config.Config, versions manifest.Manifest, commits ...Commit) map[string]*PackagePlan {
	t.Helper()
	plan, err := Build(&Input{Config: cfg, Manifest: versions, History: sameHistory(cfg, commits...), Now: now})
	require.NoError(t, err)
	byName := map[string]*PackagePlan{}
	for i := range plan.Packages {
		byName[plan.Packages[i].Name] = &plan.Packages[i]
	}
	return byName
}

func commit(sha, message string, files ...string) Commit {
	return Commit{SHA: sha, Message: message, Files: files}
}

var released = manifest.Manifest{"app-a": "1.2.0", "app-b": "2026.09.3"}

func TestSharedLibraryReleasesEveryDependent(t *testing.T) {
	cfg := mustConfig(t, monorepo)
	plans := build(t, cfg, released, commit("aaa", "feat(lib-1): add retries", "libs/lib-1/retry.go"))

	appA := plans["app-a"]
	assert.Equal(t, "1.3.0", appA.Next)
	assert.Equal(t, version.Minor, appA.Bump)
	assert.Equal(t, "app-a-v1.3.0", appA.Tag)
	require.Len(t, appA.Entries, 1)
	assert.Equal(t, []Reason{{Kind: ByDependency, Via: "lib-1", File: "libs/lib-1/retry.go"}}, appA.Entries[0].Reasons)
	assert.True(t, appA.Entries[0].Inherited())

	appB := plans["app-b"]
	assert.Equal(t, "2026.10.0", appB.Next)
	assert.Equal(t, "app-b-v2026.10.0", appB.Tag)
}

func TestLibraryOnlyReleasesItsDependents(t *testing.T) {
	cfg := mustConfig(t, monorepo)
	plans := build(t, cfg, released, commit("bbb", "fix: off by one", "libs/lib-2/a.go"))
	assert.Equal(t, "1.2.1", plans["app-a"].Next)
	assert.False(t, plans["app-b"].Releasing())
	assert.Empty(t, plans["app-b"].Entries)
}

func TestTransitiveDependency(t *testing.T) {
	cfg := mustConfig(t, monorepo)
	plans := build(t, cfg, released, commit("ccc", "perf: faster", "libs/lib-3/x.go"))
	assert.Equal(t, "1.2.1", plans["app-a"].Next)
	assert.Equal(t, "2026.10.0", plans["app-b"].Next)
	assert.Equal(t, "lib-3", plans["app-b"].Entries[0].Reasons[0].Via)
}

func TestAlsoGlobOfDependency(t *testing.T) {
	cfg := mustConfig(t, monorepo)
	plans := build(t, cfg, released, commit("ddd", "feat: new rpc", "proto/v1/api.proto"))
	assert.Equal(t, "1.3.0", plans["app-a"].Next)
	assert.Equal(
		t,
		[]Reason{{Kind: ByAlso, Via: "proto/**", File: "proto/v1/api.proto"}},
		plans["app-a"].Entries[0].Reasons,
	)
	assert.False(t, plans["app-b"].Releasing())
}

func TestExcludedFilesBelongToNobody(t *testing.T) {
	cfg := mustConfig(t, monorepo)
	plans := build(t, cfg, released, commit("eee", "fix: typo in docs", "libs/lib-2/docs/readme.md"))
	assert.False(t, plans["app-a"].Releasing())
	assert.Empty(t, plans["app-a"].Ignored)
}

func TestNonReleasableCommitsAreIgnored(t *testing.T) {
	cfg := mustConfig(t, monorepo)
	plans := build(t, cfg, released,
		commit("fff", "docs: explain", "apps/app-a/README.md"),
		commit("ggg", "Update stuff", "apps/app-a/main.go"),
		commit("hhh", "chore: tidy", "unowned/file.txt"),
	)
	appA := plans["app-a"]
	assert.False(t, appA.Releasing())
	require.Len(t, appA.Ignored, 2)
	assert.Equal(t, "docs: explain", appA.Ignored[0].Summary)
	assert.NotNil(t, appA.Ignored[0].Commit)
	assert.Equal(t, "Update stuff", appA.Ignored[1].Summary)
	assert.Nil(t, appA.Ignored[1].Commit)
}

func TestBumpTakesTheLargestChange(t *testing.T) {
	cfg := mustConfig(t, monorepo)
	plans := build(t, cfg, released,
		commit("i1", "fix: a", "apps/app-a/a.go"),
		commit("i2", "feat!: b", "apps/app-a/b.go"),
		commit("i3", "deps: bump x", "apps/app-a/go.mod"),
	)
	assert.Equal(t, version.Major, plans["app-a"].Bump)
	assert.Equal(t, "2.0.0", plans["app-a"].Next)
	assert.Len(t, plans["app-a"].Entries, 3)
	assert.Equal(t, version.Patch, plans["app-a"].Entries[2].Bump, "configured deps bump")
}

func TestBreakingBelowOneIsMinor(t *testing.T) {
	cfg := mustConfig(t, monorepo)
	plans := build(t, cfg, manifest.Manifest{"app-a": "0.3.1"},
		commit("j1", "refactor!: rework", "apps/app-a/a.go"))
	assert.Equal(t, "0.4.0", plans["app-a"].Next)
}

func TestFirstRelease(t *testing.T) {
	cfg := mustConfig(t, monorepo)
	plans := build(t, cfg, manifest.Manifest{}, commit("k1", "fix: first", "libs/lib-1/a.go"))
	assert.Empty(t, plans["app-a"].Current)
	assert.Equal(t, "0.1.0", plans["app-a"].Next)
	assert.Equal(t, "2026.10.0", plans["app-b"].Next)
}

func TestReleaseAs(t *testing.T) {
	cfg := mustConfig(t, monorepo)
	plans := build(t, cfg, released,
		commit("l2", "chore: go stable\n\nRelease-As: 2.0.0", "apps/app-a/a.go"),
		commit("l1", "chore: older\n\nRelease-As: 1.5.0", "apps/app-a/a.go"),
	)
	assert.Equal(t, "2.0.0", plans["app-a"].Next)
	assert.Equal(t, version.Patch, plans["app-a"].Bump)
}

func TestInvalidReleaseAs(t *testing.T) {
	cfg := mustConfig(t, monorepo)
	_, err := Build(&Input{
		Config: cfg, Manifest: released, Now: now,
		History: sameHistory(cfg, commit("m1", "chore: x\n\nRelease-As: two", "apps/app-a/a.go")),
	})
	require.ErrorIs(t, err, version.ErrInvalid)
}

func TestInvalidManifestVersion(t *testing.T) {
	cfg := mustConfig(t, monorepo)
	_, err := Build(&Input{Config: cfg, Manifest: manifest.Manifest{"app-b": "1.2.3"}, Now: now})
	require.ErrorIs(t, err, version.ErrInvalid)
}

func TestNestedPackagesOwnTheirFiles(t *testing.T) {
	cfg := mustConfig(t, `
[packages.root]
path = "."

[packages.plugin]
path = "plugins/plugin"
`)
	plans := build(t, cfg, manifest.Manifest{"root": "1.0.0", "plugin": "1.0.0"},
		commit("n2", "feat: plugin thing", "plugins/plugin/main.go"),
		commit("n1", "fix: root thing", "cmd/main.go", "README.md"),
	)
	assert.Equal(t, "1.0.1", plans["root"].Next)
	assert.Equal(t, "v1.0.1", plans["root"].Tag)
	require.Len(t, plans["root"].Entries, 1)
	assert.Equal(t, []Reason{{Kind: ByPath, File: "cmd/main.go"}}, plans["root"].Entries[0].Reasons)
	assert.Equal(t, "1.1.0", plans["plugin"].Next)
}

func TestReasonsAreReportedOncePerRule(t *testing.T) {
	cfg := mustConfig(t, monorepo)
	plans := build(t, cfg, released, commit("o1", "fix: everywhere",
		"apps/app-a/a.go", "apps/app-a/b.go", "libs/lib-1/a.go", "libs/lib-1/b.go", "libs/lib-2/a.go"))
	assert.Equal(t, []Reason{
		{Kind: ByPath, File: "apps/app-a/a.go"},
		{Kind: ByDependency, Via: "lib-1", File: "libs/lib-1/a.go"},
		{Kind: ByDependency, Via: "lib-2", File: "libs/lib-2/a.go"},
	}, plans["app-a"].Entries[0].Reasons)
	assert.False(t, plans["app-a"].Entries[0].Inherited())
}

func TestPlanReleases(t *testing.T) {
	cfg := mustConfig(t, monorepo)
	plan, err := Build(&Input{
		Config: cfg, Manifest: released, Now: now,
		History: sameHistory(cfg, commit("p1", "fix: a", "apps/app-a/a.go")),
	})
	require.NoError(t, err)
	require.Len(t, plan.Packages, 2)
	releases := plan.Releases()
	require.Len(t, releases, 1)
	assert.Equal(t, "app-a", releases[0].Name)
}

func TestEmptyCommitsCountForTheRootPackage(t *testing.T) {
	cfg := mustConfig(t, `
[packages.root]
path = "."

[packages.plugin]
path = "plugins/plugin"
`)
	plans := build(t, cfg, manifest.Manifest{"root": "0.9.0", "plugin": "0.9.0"},
		commit("q1", "chore: declare stable\n\nRelease-As: 1.0.0"))
	assert.Equal(t, "1.0.0", plans["root"].Next)
	assert.Equal(t, []Reason{{Kind: ByEmptyCommit}}, plans["root"].Entries[0].Reasons)
	assert.False(t, plans["root"].Entries[0].Inherited())
	assert.False(t, plans["plugin"].Releasing())
}
