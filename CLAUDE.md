# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Development Commands

This project uses [mise](https://mise.jdx.dev/) for task running and tool version management.
Run `mise tasks` to see all available tasks.
Go modules are downloaded automatically before `mise run` when `go.mod` or `go.sum` change, through mise's experimental `go` deps provider.

- **Compile**: `mise run golang:compile` - Compiles the binary for the current OS/architecture
- **Test**: `mise run test` or `go test ./...` - Runs the unit tests
- **Fix**: `mise run fix-all` - Runs all formatters and auto-fixable linters via [hk](https://hk.jdx.dev/)
- **Check**: `mise run check-all` - Runs all linters without fixing
- **CI**: `mise run ci` - Runs `check-auto` and `test`

Run `mise run fix-all` and `mise run test` before committing.

## Architecture Overview

release-bot is a CLI that releases packages from conventional commits, like release-please, but with shared code in monorepos as a first-class idea.
`ideas.local.md` (untracked) holds the background and the longer-term design.

A run does one of three things to a local repository: tags merged release commits, rebuilds the release branch, or nothing.

### Packages

- `internal/conventional` - Parses conventional commit messages
- `internal/version` - The `Bump` type and versioning schemes: semver and calver
- `internal/config` - Loads and validates `release-bot.toml`; package defaults are applied here
- `internal/manifest` - The versions manifest, a JSON map of released package to version
- `internal/planner` - Pure: from config, manifest and per-package commit history to a `Plan`, with the reason each commit counts
- `internal/changelog` - Renders a package release as markdown and prepends it to a changelog
- `internal/git` - The git CLI adapter: history, files at revisions, tags, and writing a branch with plumbing
- `internal/release` - Orchestration: `Prepare` works out what a run does, `Apply` does it.
  It declares the `Repo` interface it needs
- `cmd/release-bot` - Cobra commands `plan`, `run` and `version`, and text output

### Data Flow

1. `release.Prepare` reads the manifest at HEAD and finds each released package's last release:
   its tag, or if the tag doesn't exist yet, the commit that set the version in the manifest (that commit gets tagged)
2. It logs each package's commits since that release and hands them to `planner.Build`
3. The planner keeps the commits relevant to each package (`relevance.go`) and computes the next versions
4. `release.Prepare` renders changelogs and the new manifest into the release branch contents
5. `release.Apply` creates the tags and writes the branch

### Constraints that must not be broken

- **The working tree, index and checkout are never touched.**
  The release branch is written with `git commit-tree` on a temporary index, and refuses to rewrite the checked-out branch.
- **Runs are idempotent.**
  Tags are only created for versions without one, and an unchanged release branch is left alone.
- **The manifest at HEAD is the source of truth for released versions**, and tags anchor where each release happened.
  Release commits are found by diffing the manifest, so squash, merge and rebase merges all work.
- **The planner stays pure.**
  Anything that reads git belongs in `internal/release` or `internal/git`, so a GitHub API implementation of `release.Repo` can replace the git CLI later.
- **Generated markdown must be lint-clean.**
  The release commit is made with plumbing, so no hooks format it.

### Testing

- Unit tests use testify/assert and testify/require
- `internal/changelog/testdata` holds golden files; regenerate with `go test ./internal/changelog -update`
- Git tests are isolated from the user's git config by `internal/testing/gitrepo.Isolate`

## Import Organization

- Standard library imports first
- Third-party imports second
- Local imports last with company prefix `github.com/CallumKerson`
