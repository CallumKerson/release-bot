# release-bot

release-bot releases the packages of a repository from [conventional commits](https://www.conventionalcommits.org/).
It works like release-please, but treats shared code in monorepos as a first-class idea: a change to a shared library releases every app that depends on it.

This is a prototype.
It works on a local repository only: it creates tags and a branch, and never talks to GitHub.

## Development

This project uses [mise](https://mise.jdx.dev/) for tools and tasks, and [hk](https://hk.jdx.dev/) for linting.

```sh
mise run golang:compile   # build ./release-bot
mise run test             # unit tests
mise run fix-all          # format and fix
mise run ci               # what CI runs
```
