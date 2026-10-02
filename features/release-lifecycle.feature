Feature: From release branch to tagged release
  release-bot keeps one release branch up to date with the next versions.
  Merging that branch into main is what releases them: the next time release-bot runs,
  it finds the commit that changed the versions manifest and tags it.

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

      B  feat(lib-1): retry failed requests
         changes  libs/lib-1/retry.go
      """

  Scenario Outline: Merging the release branch tags the release, however it is merged
    Given release-bot runs
    When the release branch is merged with <merge strategy>
    And release-bot runs
    Then release-bot says:
      """
      Tagged app-a-v1.3.0 on <released commit>
      Tagged app-b-v0.5.0 on <released commit>
      """
    And these tags are created:
      | tag          | commit            |
      | app-a-v1.3.0 | <released commit> |
      | app-b-v0.5.0 | <released commit> |
    And the working tree is untouched

    Examples:
      | merge strategy | released commit  |
      | a fast-forward | release          |
      | a merge commit | release          |
      | a squash merge | squashed release |

  Scenario: Once the release is tagged there is nothing left to do
    Given release-bot runs
    And the release branch is merged with a squash merge
    And release-bot runs
    When release-bot runs
    Then release-bot says:
      """
      Nothing to release.
      """
    And no tags are created

  Scenario: Running again without new commits leaves the release branch alone
    Given release-bot runs
    When release-bot runs
    Then release-bot says:
      """
      release-bot/release is already up to date: app-a 1.3.0, app-b 0.5.0
      """
    And nothing in the repository changes

  Scenario: New commits before the release is merged rebuild the release branch
    Given release-bot runs
    And the git history continues:
      """
      C  feat(app-a)!: drop the v1 API
         changes  apps/app-a/api.go
      """
    When release-bot runs
    Then release-bot says:
      """
      Updated release-bot/release on release: app-a 2.0.0, app-b 0.5.0
      """
    And the release branch is one commit on top of main
    And the release branch changes "apps/app-a/CHANGELOG.md" to:
      """
      # Changelog

      ## 2.0.0 (2026-10-02)

      ### ⚠ BREAKING CHANGES

      - **app-a:** drop the v1 API (C)

      ### Features

      - **app-a:** drop the v1 API (C)
      - **lib-1:** retry failed requests (B) (via lib-1)
      """

  Scenario: Commits that land after the release is merged go into the next release
    Given release-bot runs
    And the release branch is merged with a squash merge
    And the git history continues:
      """
      C  fix(app-b): handle an empty basket
         changes  apps/app-b/basket.go
      """
    When release-bot runs
    Then release-bot says:
      """
      Tagged app-a-v1.3.0 on squashed release
      Tagged app-b-v0.5.0 on squashed release
      Updated release-bot/release on release: app-b 0.5.1
      """
    And the release branch releases:
      | package | from  | to    |
      | app-b   | 0.5.0 | 0.5.1 |
    And the release branch changes "apps/app-b/CHANGELOG.md" to:
      """
      # Changelog

      ## 0.5.1 (2026-10-02)

      ### Bug Fixes

      - **app-b:** handle an empty basket (C)

      ## 0.5.0 (2026-10-02)

      ### Features

      - **lib-1:** retry failed requests (B) (via lib-1)
      """

  Scenario: Commits merged from feature branches with merge commits are released
    Given the branch "feature/export" starts from B with the history:
      """
      C  feat(app-a): export to CSV
         changes  apps/app-a/export.go

      D  test(app-a): cover the export
         changes  apps/app-a/export_test.go
      """
    And "feature/export" is merged into main with a merge commit
    When release-bot plans
    Then release-bot says:
      """
      app-a: 1.2.0 -> 1.3.0 (minor)
        + feat(app-a): export to CSV (C)
            apps/app-a/export.go is in app-a
        + feat(lib-1): retry failed requests (B)
            libs/lib-1/retry.go is in lib-1, which app-a depends on
        - test(app-a): cover the export (D), test commits aren't released
      app-b: 0.4.0 -> 0.5.0 (minor)
        + feat(lib-1): retry failed requests (B)
            libs/lib-1/retry.go is in lib-1, which app-b depends on

      Release branch release-bot/release would change:
        .release-bot-manifest.json
        apps/app-a/CHANGELOG.md
        apps/app-b/CHANGELOG.md
      """

  Scenario: A dry run says what would happen without doing it
    Given release-bot runs
    And the release branch is merged with a squash merge
    And the git history continues:
      """
      C  fix(app-b): handle an empty basket
         changes  apps/app-b/basket.go
      """
    When release-bot runs with --dry-run
    Then release-bot says:
      """
      Would tag app-a-v1.3.0 on squashed release
      Would tag app-b-v0.5.0 on squashed release
      Would update release-bot/release: app-b 0.5.1
      """
    And nothing in the repository changes
