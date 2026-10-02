Feature: Releasing a repository as a single package
  When the whole repository is one package, every releasable commit counts toward its next version.
  The version goes up according to the conventional commit types since the last release.

  Background:
    Given today is 2026-10-02
    And the release config:
      """toml
      [packages.my-tool]
      path = "."
      """

  Scenario: The first release is 0.1.0
    Given the git history:
      """
      A  chore: initial commit
         changes  README.md

      B  feat: greet the user
         changes  cmd/main.go
      """
    When release-bot runs
    Then release-bot says:
      """
      Updated release-bot/release on release: my-tool 0.1.0
      """
    And the release branch releases:
      | package | from       | to    |
      | my-tool | unreleased | 0.1.0 |
    And the release branch changes "CHANGELOG.md" to:
      """
      # Changelog

      ## 0.1.0 (2026-10-02)

      ### Features

      - greet the user (B)
      """
    And the release branch changes ".release-bot-manifest.json" to:
      """
      {
        "my-tool": "0.1.0"
      }
      """
    And the release commit message is:
      """
      chore(release): my-tool 0.1.0

      - my-tool unreleased -> 0.1.0
      """
    And the release branch is one commit on top of main
    And no tags are created

  Scenario: Building the release branch never touches the working tree
    Given the git history:
      """
      A  fix: handle empty input
         changes  main.go
      """
    And someone has uncommitted work in progress
    When release-bot runs
    Then the release branch releases:
      | package | from       | to    |
      | my-tool | unreleased | 0.1.0 |
    And the working tree is untouched

  Scenario Outline: The commit type decides how far the version goes up
    Given the git history:
      """
      A  chore: release 1.4.2
         manifest  my-tool 1.4.2
         tags      v1.4.2

      B  <commit message>
         changes  main.go
      """
    When release-bot runs
    Then the release branch releases:
      | package | from  | to             |
      | my-tool | 1.4.2 | <next version> |

    Examples:
      | commit message                 | next version |
      | fix: handle empty input        | 1.4.3        |
      | perf: cache the parsed config  | 1.4.3        |
      | revert: undo the cache         | 1.4.3        |
      | feat: add a --quiet flag       | 1.5.0        |
      | feat(cli)!: rename every flag  | 2.0.0        |
      | refactor!: drop the v1 config  | 2.0.0        |

  Scenario: The biggest change since the last release wins
    Given the git history:
      """
      A  chore: release 1.4.2
         manifest  my-tool 1.4.2
         tags      v1.4.2

      B  fix: handle empty input
         changes  main.go

      C  feat: add a --quiet flag
         changes  flags.go

      D  docs: explain --quiet
         changes  README.md
      """
    When release-bot runs
    Then the release branch releases:
      | package | from  | to    |
      | my-tool | 1.4.2 | 1.5.0 |
    And the release branch changes "CHANGELOG.md" to:
      """
      # Changelog

      ## 1.5.0 (2026-10-02)

      ### Features

      - add a --quiet flag (C)

      ### Bug Fixes

      - handle empty input (B)
      """

  Scenario: Before 1.0.0 a breaking change only goes up a minor version
    Given the git history:
      """
      A  chore: release 0.3.1
         manifest  my-tool 0.3.1
         tags      v0.3.1

      B  feat: read config from TOML
         changes   config.go
         footer    BREAKING CHANGE: the JSON config file is no longer read
      """
    When release-bot runs
    Then the release branch releases:
      | package | from  | to    |
      | my-tool | 0.3.1 | 0.4.0 |
    And the release branch changes "CHANGELOG.md" to:
      """
      # Changelog

      ## 0.4.0 (2026-10-02)

      ### ⚠ BREAKING CHANGES

      - the JSON config file is no longer read (B)

      ### Features

      - read config from TOML (B)
      """

  Scenario: A Release-As footer chooses the next version
    Given the git history:
      """
      A  chore: release 0.9.0
         manifest  my-tool 0.9.0
         tags      v0.9.0

      B  chore: declare the API stable
         footer  Release-As: 1.0.0
      """
    When release-bot runs
    Then the release branch releases:
      | package | from  | to    |
      | my-tool | 0.9.0 | 1.0.0 |

  Scenario: A Release-As footer can't go back to an older version
    Given the git history:
      """
      A  chore: release 1.2.0
         manifest  my-tool 1.2.0
         tags      v1.2.0

      B  chore: go back to the old numbering
         footer  Release-As: 0.5.0
      """
    When release-bot runs
    Then release-bot fails, saying "next version 0.5.0 must be after the current version 1.2.0"
    And nothing in the repository changes

  Scenario: Commits that aren't releasable don't make a release
    Given the git history:
      """
      A  chore: release 1.0.0
         manifest  my-tool 1.0.0
         tags      v1.0.0

      B  docs: explain the flags
         changes  README.md

      C  ci: run the tests on Windows
         changes  .github/workflows/ci.yaml

      D  Tidy up some things
         changes  main.go
      """
    When release-bot runs
    Then release-bot says:
      """
      Nothing to release.
      """
    And there is no release branch
    And no tags are created

  Scenario: The plan explains each commit
    Given the git history:
      """
      A  chore: release 1.0.0
         manifest  my-tool 1.0.0
         tags      v1.0.0

      B  docs: explain the flags
         changes  README.md

      C  fix: exit non-zero on errors
         changes  main.go

      D  Tidy up some things
         changes  main.go
      """
    When release-bot plans
    Then release-bot says:
      """
      my-tool: 1.0.0 -> 1.0.1 (patch)
        + fix: exit non-zero on errors (C)
            main.go is in my-tool
        - Tidy up some things (D), not a conventional commit
        - docs: explain the flags (B), docs commits aren't released

      Release branch release-bot/release would change:
        .release-bot-manifest.json
        CHANGELOG.md
      """
    And nothing in the repository changes
