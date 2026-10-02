Feature: Configuration mistakes are reported, not guessed at
  release-bot refuses to run when its config or versions manifest doesn't make sense,
  and says what is wrong.

  Background:
    Given today is 2026-10-02

  Scenario: A package that depends on a package that doesn't exist
    Given the release config:
      """toml
      [packages.app]
      path = "app"
      depends-on = ["lib"]
      """
    And the git history:
      """
      A  feat: start
         changes  app/main.go
      """
    When release-bot runs
    Then release-bot fails, saying "packages.app.depends-on: unknown package \"lib\""

  Scenario: Packages that depend on each other in a loop
    Given the release config:
      """toml
      [packages.app]
      path = "app"
      depends-on = ["lib-a"]

      [packages.lib-a]
      path = "libs/a"
      release = false
      depends-on = ["lib-b"]

      [packages.lib-b]
      path = "libs/b"
      release = false
      depends-on = ["lib-a"]
      """
    And the git history:
      """
      A  feat: start
         changes  app/main.go
      """
    When release-bot runs
    Then release-bot fails, saying "depends-on cycle"

  Scenario: The manifest lists a package that isn't released
    Given the release config:
      """toml
      [packages.app]
      path = "app"

      [packages.lib]
      path = "lib"
      release = false
      """
    And the git history:
      """
      A  chore: release
         changes   app/main.go
         manifest  app 1.0.0, lib 1.0.0
      """
    When release-bot runs
    Then release-bot fails, saying ".release-bot-manifest.json lists \"lib\", which isn't a released package"

  Scenario: The manifest has a version the package's scheme can't read
    Given the release config:
      """toml
      [packages.app]
      path = "app"
      """
    And the git history:
      """
      A  chore: release
         changes   app/main.go
         manifest  app v1

      B  fix: something
         changes  app/main.go
      """
    When release-bot runs
    Then release-bot fails, saying "\"v1\" is not a MAJOR.MINOR.PATCH semantic version"

  Scenario: An unknown setting is probably a typo
    Given the release config:
      """toml
      [packages.app]
      path = "app"
      depends_on = ["lib"]
      """
    And the git history:
      """
      A  feat: start
         changes  app/main.go
      """
    When release-bot runs
    Then release-bot fails, saying "depends_on"
