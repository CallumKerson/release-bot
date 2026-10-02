Feature: Releasing on GitHub
  With --github, release-bot works with GitHub the way release-please does.
  It pushes the release branch and proposes it in a release pull request.
  Merging that pull request is what releases it: the next run tags the merged release,
  pushes the tags, and publishes a GitHub release of each, with its notes from the changelog.

  The repository in each scenario is the checkout a GitHub Actions workflow would have,
  with a fake GitHub as its origin, and the token, repository and API URL in the environment.
  Commits in the git history are pushed to main on GitHub as they are made.

  Background:
    Given today is 2026-10-02
    And the release config:
      """toml
      [packages.app-a]
      path = "apps/app-a"
      depends-on = ["lib-1"]

      [packages.app-b]
      path = "apps/app-b"
      depends-on = ["lib-1"]

      [packages.lib-1]
      path = "libs/lib-1"
      release = false
      """
    And the git history:
      """
      A  chore: release app-a 1.2.0 and app-b 0.4.0
         changes   apps/app-a/main.go, apps/app-b/main.go, libs/lib-1/lib.go
         manifest  app-a 1.2.0, app-b 0.4.0
         tags      app-a-v1.2.0, app-b-v0.4.0
      """
    And the repository is on GitHub, with a release of each tag
    And the git history continues:
      """
      B  feat(lib-1): retry failed requests
         changes  libs/lib-1/retry.go

      C  fix(app-b): handle an empty basket
         changes  apps/app-b/basket.go
      """

  Scenario: Releasable commits open a release pull request
    When release-bot runs with --github
    Then release-bot says:
      """
      Updated release-bot/release on release: app-a 1.3.0, app-b 0.5.0
      Pushed release-bot/release
      Opened pull request #1: https://github.com/octo-org/widgets/pull/1
      """
    And the release branch is pushed
    And the release pull request is open, titled "chore(release): app-a 1.3.0, app-b 0.5.0"
    And the release pull request says:
      """
      ## app-a 1.3.0

      ### Features

      - **lib-1:** retry failed requests (B) (via lib-1)

      ## app-b 0.5.0

      ### Features

      - **lib-1:** retry failed requests (B) (via lib-1)

      ### Bug Fixes

      - **app-b:** handle an empty basket (C)

      """
    And no GitHub releases are published

  Scenario: New commits update the open release pull request
    Given release-bot runs with --github
    And the git history continues:
      """
      D  feat(app-a)!: drop the v1 API
         changes  apps/app-a/api.go
      """
    When release-bot runs with --github
    Then release-bot says:
      """
      Updated release-bot/release on release: app-a 2.0.0, app-b 0.5.0
      Pushed release-bot/release
      Updated pull request #1: https://github.com/octo-org/widgets/pull/1
      """
    And the release branch is pushed
    And the release pull request is open, titled "chore(release): app-a 2.0.0, app-b 0.5.0"

  Scenario: Running again changes nothing on GitHub
    Given release-bot runs with --github
    When release-bot runs with --github
    Then release-bot says:
      """
      release-bot/release is already up to date: app-a 1.3.0, app-b 0.5.0
      Pull request #1 is already up to date: https://github.com/octo-org/widgets/pull/1
      """
    And nothing on GitHub changes

  Scenario Outline: Merging the release pull request publishes the releases, however it is merged
    Given release-bot runs with --github
    When the release pull request is merged with <merge strategy>
    And release-bot runs with --github
    Then release-bot says:
      """
      Tagged app-a-v1.3.0 on <released commit>
      Tagged app-b-v0.5.0 on <released commit>
      Published release app-a-v1.3.0: https://github.com/octo-org/widgets/releases/tag/app-a-v1.3.0
      Published release app-b-v0.5.0: https://github.com/octo-org/widgets/releases/tag/app-b-v0.5.0
      """
    And these GitHub releases are published:
      | tag          | commit            |
      | app-a-v1.3.0 | <released commit> |
      | app-b-v0.5.0 | <released commit> |
    And the GitHub release "app-b-v0.5.0" says:
      """
      ### Features

      - **lib-1:** retry failed requests (B) (via lib-1)

      ### Bug Fixes

      - **app-b:** handle an empty basket (C)
      """

    Examples:
      | merge strategy | released commit  |
      | a merge commit | release          |
      | a squash merge | squashed release |
      | a rebase       | rebased release  |

  Scenario: Once the releases are published there is nothing left to do
    Given release-bot runs with --github
    And the release pull request is merged with a squash merge
    And release-bot runs with --github
    When release-bot runs with --github
    Then release-bot says:
      """
      Nothing to release.
      """
    And nothing on GitHub changes

  Scenario: A run that stopped after pushing the tags is finished by the next
    Given release-bot runs with --github
    And the release pull request is merged with a squash merge
    And release-bot runs
    And the tags are pushed to GitHub
    When release-bot runs with --github
    Then release-bot says:
      """
      Nothing to release.
      Published release app-a-v1.3.0: https://github.com/octo-org/widgets/releases/tag/app-a-v1.3.0
      Published release app-b-v0.5.0: https://github.com/octo-org/widgets/releases/tag/app-b-v0.5.0
      """
    And these GitHub releases are published:
      | tag          | commit           |
      | app-a-v1.3.0 | squashed release |
      | app-b-v0.5.0 | squashed release |

  Scenario: A dry run says what it would do on GitHub without doing it
    Given release-bot runs with --github
    And the release pull request is merged with a squash merge
    When release-bot runs with --github --dry-run
    Then release-bot says:
      """
      Would tag app-a-v1.3.0 on squashed release
      Would tag app-b-v0.5.0 on squashed release
      Would publish release app-a-v1.3.0
      Would publish release app-b-v0.5.0
      """
    And nothing in the repository changes
    And nothing on GitHub changes

  Scenario: Without a token, release-bot fails before changing anything
    Given there is no GitHub token
    When release-bot runs with --github
    Then release-bot fails, saying "invalid GitHub settings: no token, set GITHUB_TOKEN"
    And nothing in the repository changes
    And nothing on GitHub changes
