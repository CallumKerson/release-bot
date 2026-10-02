# release-bot

release-bot releases the packages of a repository from [conventional commits](https://www.conventionalcommits.org/).
It works like release-please, but treats shared code in monorepos as a first-class idea: a change to a shared library releases every app that depends on it.

This is a prototype.
It works on a local repository only: it creates tags and a branch, and never talks to GitHub.

## What a run does

`release-bot run` does exactly one of three things:

1. **Tags merged releases.**
   When a commit since the last run changed versions in the manifest, that commit is a release. release-bot tags it, for example `app-a-v1.3.0`.
2. **Rebuilds the release branch.**
   When there are releasable commits since a package's last release, release-bot builds `release-bot/release`: one commit on top of `HEAD` that updates each released package's `CHANGELOG.md` and the manifest.
   Merging that branch is how a release happens.
3. **Nothing**, when no commit since the last release is releasable.

It never touches the working tree, the index or the checked-out branch.

`release-bot plan` explains what `run` would do and why, commit by commit, without changing anything.
Both commands take `--json` for machine-readable output.

## Configuration

`release-bot.toml` in the repository root (or `.config/release-bot.toml`):

```toml
branch   = "release-bot/release"           # optional, default shown
manifest = ".release-bot-manifest.json"    # optional, default shown

[defaults]                                 # applied to every package
scheme = "semver"                          # "semver" or "calver"
tag    = "{name}-v{version}"               # tokens: {name} {path} {version}

[bump]                                     # optional: make more commit types releasable
deps = "patch"

[packages.app-a]
path       = "apps/app-a"
depends-on = ["lib-1", "lib-2"]

[packages.app-b]
path          = "apps/app-b"
depends-on    = ["lib-1"]
scheme        = "calver"
calver-format = "YYYY.0M.MICRO"

[packages.lib-1]
path    = "libs/lib-1"
release = false                            # shared code: never tagged or versioned

[packages.lib-2]
path    = "libs/lib-2"
release = false
also    = ["proto/**"]                     # files outside the directory that also count
exclude = ["libs/lib-2/docs/**"]           # files inside it that don't
```

| Key               | Default                 | Meaning                                                              |
| ----------------- | ----------------------- | -------------------------------------------------------------------- |
| `path`            | required                | The package's directory; `"."` is the whole repository                |
| `release`         | `true`                  | `false` for shared code that only matters to its dependents           |
| `depends-on`      | `[]`                    | Packages whose changes count as changes to this one, transitively    |
| `also`            | `[]`                    | Globs of other files whose changes count as changes to this one      |
| `exclude`         | `[]`                    | Globs of files in the directory whose changes don't count            |
| `scheme`          | `semver`                | `semver` or `calver`                                                 |
| `calver-format`   | `YYYY.0M.MICRO`         | [calver.org](https://calver.org) tokens                              |
| `initial-version` | `0.1.0`                 | The first semver release                                             |
| `tag`             | `{name}-v{version}`     | Tag template; the root package defaults to `v{version}`             |
| `changelog`       | `<path>/CHANGELOG.md`   | Where release notes go                                               |

Each file belongs to the package with the longest path containing it.
A commit counts toward a package when it changes a file that package owns, a file owned by one of its dependencies, or a file matching its or its dependencies' `also` globs.
An empty commit counts toward the root package, so `git commit --allow-empty -m "chore: release 2.0.0" -m "Release-As: 2.0.0"` works as it does with release-please.

The manifest maps each released package to its current version, `{"app-a": "1.2.0", "app-b": "2026.09.0"}`.
release-bot writes it; for an existing repository, start it with the current versions and tag that commit.

## Versions

Semantic versions go up by the largest change since the last release:

| Commit                                    | Bump  | Below 1.0.0 |
| ----------------------------------------- | ----- | ----------- |
| `feat!:`, or a `BREAKING CHANGE:` footer  | major | minor       |
| `feat:`                                   | minor | minor       |
| `fix:`, `perf:`, `revert:`                | patch | patch       |
| anything else                             | none  | none        |

A `Release-As: x.y.z` footer sets the next version outright, as long as it is after the current version.
Calendar versions take their date from the day of the release, and count releases within the same date with `MICRO`.

## Behaviour, by example

The [features](features/README.md) directory describes release-bot's behaviour as plain-language scenarios, each with the git history it starts from.
They double as the integration tests.

## Development

This project uses [mise](https://mise.jdx.dev/) for tools and tasks, and [hk](https://hk.jdx.dev/) for linting.

```sh
mise run golang:compile   # build ./release-bot
mise run test             # unit tests and feature scenarios
mise run fix-all          # format and fix
mise run ci               # what CI runs
```
