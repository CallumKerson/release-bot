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

- `cmd/release-bot` - Cobra commands

## Import Organization

- Standard library imports first
- Third-party imports second
- Local imports last with company prefix `github.com/CallumKerson`
