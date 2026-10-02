Feature: Releasing apps that share libraries
  In a monorepo some directories are apps that get released, and some are libraries of shared code.
  A change to a library is a change to every app that depends on it, directly or through another library,
  so those apps are released as if the change had been made in the app itself.

  Here app-a uses lib-1 and lib-2, and app-b uses only lib-1.
  lib-1 is built on lib-3, and lib-2 also covers the shared protobuf definitions.

  Background:
    Given today is 2026-10-02
    And the release config:
      """toml
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
      """
    And the git history:
      """
      A  chore: release app-a 1.2.0 and app-b 2026.09.0
         changes   apps/app-a/main.go, apps/app-b/main.go, libs/lib-1/lib.go, libs/lib-2/lib.go, libs/lib-3/lib.go
         manifest  app-a 1.2.0, app-b 2026.09.0
         tags      app-a-v1.2.0, app-b-v2026.09.0
      """

  Scenario: A feature in lib-1 releases both apps
    Given the git history continues:
      """
      B  feat(lib-1): retry failed requests
         changes  libs/lib-1/retry.go
      """
    When release-bot runs
    Then release-bot says:
      """
      Updated release-bot/release on release: app-a 1.3.0, app-b 2026.10.0
      """
    And the release branch releases:
      | package | from      | to        |
      | app-a   | 1.2.0     | 1.3.0     |
      | app-b   | 2026.09.0 | 2026.10.0 |
    And the release branch changes "apps/app-a/CHANGELOG.md" to:
      """
      # Changelog

      ## 1.3.0 (2026-10-02)

      ### Features

      - **lib-1:** retry failed requests (B) (via lib-1)
      """
    And the release branch changes "apps/app-b/CHANGELOG.md" to:
      """
      # Changelog

      ## 2026.10.0 (2026-10-02)

      ### Features

      - **lib-1:** retry failed requests (B) (via lib-1)
      """
    And the release commit message is:
      """
      chore(release): app-a 1.3.0, app-b 2026.10.0

      - app-a 1.2.0 -> 1.3.0
      - app-b 2026.09.0 -> 2026.10.0
      """

  Scenario: A fix in lib-2 only releases app-a, the app that uses it
    Given the git history continues:
      """
      B  fix(lib-2): close the connection
         changes  libs/lib-2/conn.go
      """
    When release-bot runs
    Then the release branch releases:
      | package | from  | to    |
      | app-a   | 1.2.0 | 1.2.1 |

  Scenario: A change to lib-3 reaches both apps through lib-1
    Given the git history continues:
      """
      B  perf(lib-3): reuse buffers
         changes  libs/lib-3/buffer.go
      """
    When release-bot runs
    Then the release branch releases:
      | package | from      | to        |
      | app-a   | 1.2.0     | 1.2.1     |
      | app-b   | 2026.09.0 | 2026.10.0 |

  Scenario: A change in app-b only releases app-b
    Given the git history continues:
      """
      B  feat(app-b): dark mode
         changes  apps/app-b/theme.go
      """
    When release-bot runs
    Then the release branch releases:
      | package | from      | to        |
      | app-b   | 2026.09.0 | 2026.10.0 |
    And the release branch changes "apps/app-b/CHANGELOG.md" to:
      """
      # Changelog

      ## 2026.10.0 (2026-10-02)

      ### Features

      - **app-b:** dark mode (B)
      """

  Scenario: Files outside every package can still be shared with "also"
    Given the git history continues:
      """
      B  feat: add the Cancel RPC
         changes  proto/v1/service.proto
      """
    When release-bot runs
    Then the release branch releases:
      | package | from  | to    |
      | app-a   | 1.2.0 | 1.3.0 |
    And the release branch changes "apps/app-a/CHANGELOG.md" to:
      """
      # Changelog

      ## 1.3.0 (2026-10-02)

      ### Features

      - add the Cancel RPC (B) (via `proto/**`)
      """

  Scenario: Excluded files don't release anything
    Given the git history continues:
      """
      B  fix(lib-2): correct the docs example
         changes  libs/lib-2/docs/example.md
      """
    When release-bot runs
    Then release-bot says:
      """
      Nothing to release.
      """
    And there is no release branch

  Scenario: Each app's changelog lists only the commits that affect it
    Given the git history continues:
      """
      B  feat(app-a): export to CSV
         changes  apps/app-a/export.go

      C  fix(lib-1): handle timeouts
         changes  libs/lib-1/client.go

      D  feat(app-b)!: remove the legacy API
         changes  apps/app-b/api.go
      """
    When release-bot runs
    Then the release branch releases:
      | package | from      | to        |
      | app-a   | 1.2.0     | 1.3.0     |
      | app-b   | 2026.09.0 | 2026.10.0 |
    And the release branch changes "apps/app-a/CHANGELOG.md" to:
      """
      # Changelog

      ## 1.3.0 (2026-10-02)

      ### Features

      - **app-a:** export to CSV (B)

      ### Bug Fixes

      - **lib-1:** handle timeouts (C) (via lib-1)
      """
    And the release branch changes "apps/app-b/CHANGELOG.md" to:
      """
      # Changelog

      ## 2026.10.0 (2026-10-02)

      ### ⚠ BREAKING CHANGES

      - **app-b:** remove the legacy API (D)

      ### Features

      - **app-b:** remove the legacy API (D)

      ### Bug Fixes

      - **lib-1:** handle timeouts (C) (via lib-1)
      """

  Scenario: The plan explains why each app is released
    Given the git history continues:
      """
      B  feat(lib-1): retry failed requests
         changes  libs/lib-1/retry.go

      C  fix: tighten the RPC schema
         changes  proto/v1/service.proto

      D  docs(app-b): describe the themes
         changes  apps/app-b/README.md
      """
    When release-bot plans
    Then release-bot says:
      """
      app-a: 1.2.0 -> 1.3.0 (minor)
        + fix: tighten the RPC schema (C)
            proto/v1/service.proto matches proto/**
        + feat(lib-1): retry failed requests (B)
            libs/lib-1/retry.go is in lib-1, which app-a depends on
      app-b: 2026.09.0 -> 2026.10.0 (minor)
        + feat(lib-1): retry failed requests (B)
            libs/lib-1/retry.go is in lib-1, which app-b depends on
        - docs(app-b): describe the themes (D), docs commits aren't released

      Release branch release-bot/release would change:
        .release-bot-manifest.json
        apps/app-a/CHANGELOG.md
        apps/app-b/CHANGELOG.md
      """
    And nothing in the repository changes
