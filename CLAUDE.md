# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Development Commands

This project uses [mise](https://mise.jdx.dev/) for task running and tool version management.
Run `mise tasks` to see all available tasks.
Go modules are downloaded automatically before `mise run` when `go.mod` or `go.sum` change, through mise's experimental `go` deps provider.

- **Compile**: `mise run golang:compile` - Compiles the binary for the current OS/architecture
- **Test**: `mise run test` or `go test ./...` - Runs unit tests and the feature scenarios
- **Fix**: `mise run fix-all` - Runs all formatters and auto-fixable linters via [hk](https://hk.jdx.dev/)
- **Check**: `mise run check-all` - Runs all linters without fixing
- **CI**: `mise run ci` - Runs `check-auto` and `test`

Run `mise run fix-all` and `mise run test` before committing.

## Architecture Overview

release-bot is a CLI that releases packages from conventional commits, like release-please, but with shared code in monorepos as a first-class idea.
`ideas.local.md` (untracked) holds the background and the longer-term design.

A run does one of three things to a repository: tags merged release commits, rebuilds the release branch, or nothing.
Without `--github` that repository is the local one, read and written with the git CLI.
With `--github` it is the GitHub repository, read and written through the API alone, so no checkout is needed; the run then publishes GitHub releases, and opens or updates the release pull request.

### Packages

- `internal/conventional` - Parses conventional commit messages
- `internal/version` - The `Bump` type and versioning schemes: semver and calver
- `internal/config` - Loads and validates `release-bot.toml`; package defaults are applied here
- `internal/manifest` - The versions manifest, a JSON map of released package to version
- `internal/planner` - Pure: from config, manifest and per-package commit history to a `Plan`, with the reason each commit counts
- `internal/changelog` - Renders a package release as markdown and prepends it to a changelog
- `internal/vcs` - The `Commit` type, with its parents, that version control adapters return
- `internal/git` - The git CLI adapter: history, files at revisions, tags, and writing a branch with plumbing
- `internal/release` - Orchestration: `Prepare` works out what a run does, `Apply` does it to the repository, `Publish` publishes releases and the pull request.
  It declares the `Repo` and `Host` interfaces it needs
- `internal/release/releasetest` - In-memory `release.Repo` and `release.Host` fakes, and the contract tests each implementation must pass
- `internal/github` - The GitHub `release.Repo` and `release.Host`, over go-github: history, files, tags and the release branch through the git database API, release pull requests, and releases
- `internal/github/githubtest` - A fake GitHub REST API over a bare git repository, for tests
- `internal/cli` - Cobra commands `plan`, `run` and `version`, and text output
- `cmd/release-bot` - `main`, which runs `internal/cli`

### Data Flow

1. `release.Prepare` reads the manifest at HEAD and finds each released package's last release:
   its tag, or if the tag doesn't exist yet, the commit that set the version in the manifest (that commit gets tagged)
2. It logs each package's commits since that release and hands them to `planner.Build`
3. The planner keeps the commits relevant to each package (`relevance.go`) and computes the next versions
4. `release.Prepare` renders changelogs and the new manifest into the release branch contents
5. `release.Apply` creates the tags and writes the branch
6. With `--github`, where `Apply` already wrote to GitHub, `release.Publish` publishes a release of every current version that lacks one,
   then ensures the branch's pull request

### Constraints that must not be broken

- **The working tree, index and checkout are never touched.**
  Locally, the release branch is written with `git commit-tree` on a temporary index, and refuses to rewrite the checked-out branch.
  On GitHub, it is written with the git database API, and refuses to rewrite the target branch.
- **`--github` needs no checkout.**
  Everything it reads and writes, including the config, goes through the API, and the `release.Repo` contract has no revision syntax such as `sha^` so the API can implement it.
  The release commit has no author or committer, so GitHub signs it for bot tokens.
- **Runs are idempotent.**
  Tags are only created for versions without one, and an unchanged release branch is left alone.
  On GitHub, releases are only published for tags without one, and an unchanged pull request isn't edited.
- **The manifest at HEAD is the source of truth for released versions**, and tags anchor where each release happened.
  Release commits are found by diffing the manifest, so squash, merge and rebase merges all work.
- **The planner stays pure.**
  Anything that reads a repository belongs behind `release.Repo`, in `internal/git` or `internal/github`.
- **Generated markdown must be lint-clean.**
  The release commit is made with plumbing, so no hooks format it.

### Testing

- Unit tests use testify/assert and testify/require
- `internal/release` is unit tested against `releasetest.Fake` and `releasetest.FakeHost`.
  `releasetest.RunContract` runs against the fake, the git adapter and the GitHub adapter, and `RunHostContract` against both `FakeHost` and the GitHub adapter; any new implementation should run them too
- Nothing runs against the real GitHub, so `githubtest` aims to behave like it: go-github's types on the wire, refs looked up in a real bare repository, GitHub's documented errors, and merges made as GitHub makes them.
  Requests it doesn't serve fail the test, so a new endpoint needs adding to the fake, with a link to GitHub's docs
- `internal/changelog/testdata` holds golden files; regenerate with `go test ./internal/changelog -update`
- `features/*.feature` are [godog](https://github.com/cucumber/godog) scenarios run by `features/features_test.go`.
  They are the integration tests and the behaviour spec, written to be read by non-experts; see `features/README.md`.
  Each scenario builds a temporary repository from a git history written in the feature, then runs the real commands in-process.
  Commit hashes in output are replaced by the history's commit labels.
  `features/github.feature` puts the repository on a `githubtest` fake as its origin, with the environment a workflow provides, and runs release-bot from an empty directory
- Git tests are isolated from the user's git config by `internal/testing/gitrepo.Isolate`

## Import Organization

- Standard library imports first
- Third-party imports second
- Local imports last with company prefix `github.com/CallumKerson`
