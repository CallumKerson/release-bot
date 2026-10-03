# Features

Each `.feature` file describes how release-bot behaves, as examples written in [Gherkin](https://cucumber.io/docs/gherkin/reference/).
They are also the integration tests: `go test ./features` runs every scenario against a real, temporary git repository.

## Reading a scenario

- **Given** steps set up a repository: the release config, today's date and the git history.
- **When** steps run release-bot, or merge its release branch.
- **Then** steps say what should have happened.

## The git history

Histories are written like a short `git log`, oldest commit first:

```text
A  chore: first release
   changes   apps/app-a/main.go, libs/lib-1/lib.go
   manifest  app-a 1.2.0
   tags      app-a-v1.2.0

B  feat(lib-1): add retries
   changes   libs/lib-1/retry.go
   footer    BREAKING CHANGE: retries are on by default
```

Each commit starts with a label, here `A` and `B`, followed by its commit message.
The indented lines below it describe the commit, and list several files, versions or tags separated by commas:

| Keyword    | Meaning                                                                          |
| ---------- | -------------------------------------------------------------------------------- |
| `changes`  | Files the commit creates or edits                                                |
| `deletes`  | Files the commit deletes                                                         |
| `manifest` | The versions the commit writes to the versions manifest, as `package version`    |
| `tags`     | Tags on the commit                                                               |
| `body`     | A line of the commit message body                                                |
| `footer`   | A footer line at the end of the commit message, such as `Release-As: 2.0.0`     |

Blank lines and lines starting with `#` are ignored.
The release config is committed with the first commit.

## Commit labels in expectations

Commit hashes change on every run, so wherever release-bot prints or writes a commit hash, the scenario shows its label instead.
A changelog line `- add retries (B)` means the line ends with the hash of commit `B`.

release-bot's own commits get labels too:

- `release` is the commit release-bot puts on the release branch.
- `squashed release` is the commit on main made by squash merging the release branch.
- `merge of <branch>` is the merge commit made by merging a branch with a merge commit.
- `rebased release` is the commit on main made by rebase merging the release pull request on GitHub.

## GitHub

`Given the repository is on GitHub` gives the scenario's repository a fake GitHub as its origin. release-bot gets the token, repository and API URL from the environment, the way a workflow provides them.
From then on, commits in the git history are pushed to main on GitHub as they are made.
With `--github`, release-bot runs from an empty directory, as it works through GitHub's API alone and needs no checkout.

The fake GitHub runs in process, over a bare git repository, so the scenarios need no network or account.
In end-to-end mode the same scenarios run against a real GitHub repository instead (see below).
`When the release pull request is merged with …` merges it on GitHub the way GitHub's merge button does, then updates the scenario's repository to the new main, so later steps can name its commits.

## Running

```sh
go test ./features -run TestFeatures                         # every scenario
go test ./features -run 'TestFeatures/A_feature_in_lib-1'     # scenarios whose name starts with this
go test ./features -run TestFeatures -v -godog.format=pretty  # print each step as it runs
```

### End to end

`mise run e2e` runs the GitHub scenarios against the real [release-bot-test-repo](https://github.com/CallumKerson/release-bot-test-repo).
Each scenario resets that repository: it closes open pull requests, deletes every release, and force pushes the scenario's branches and tags in place of the ones there.
Set `RELEASE_BOT_E2E_REPOSITORY` to use another, but only one that exists for this.

It runs as a GitHub App installed on the repository, because GitHub only signs release-bot's commits for an app or a workflow's token. [fnox](https://fnox.jdx.dev) reads the app's client ID and private key from 1Password, as `fnox.toml` says.
`RELEASE_BOT_E2E_TOKEN` runs it with a token instead.

The repository's name and pull request numbers are shown as the fake's, so the scenarios read the same in both modes.
Steps that read GitHub's lists retry for a few seconds, as a real GitHub can take a moment to list what release-bot just did.

On CI, the `e2e` job runs them on every pull request once the other checks pass, as the same GitHub App, with its credentials in the `e2e` environment.
There is only one test repository, so the job waits its turn in a queue for it, and a new push to a pull request cancels that pull request's earlier run.

```sh
mise run e2e                                 # every GitHub scenario
mise run e2e -- -v -godog.format=pretty      # print each step as it runs
```
