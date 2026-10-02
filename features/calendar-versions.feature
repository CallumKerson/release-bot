Feature: Calendar versions
  A package can use calendar versioning instead of semantic versioning.
  Its version is built from the release date, using the tokens from calver.org,
  with a MICRO counter for releases that would otherwise get the same version.
  Any releasable commit makes a release; the kind of change doesn't matter.

  Scenario Outline: The version comes from today's date
    Given today is <today>
    And the release config:
      """toml
      [packages.site]
      path = "."
      scheme = "calver"
      calver-format = "<format>"
      """
    And the git history:
      """
      A  chore: release <last version>
         manifest  site <last version>
         tags      v<last version>

      B  fix: correct a broken link
         changes  index.html
      """
    When release-bot runs
    Then the release branch releases:
      | package | from           | to             |
      | site    | <last version> | <next version> |

    Examples:
      | format           | today      | last version | next version | why                                     |
      | YYYY.0M.MICRO    | 2026-10-02 | 2026.10.0    | 2026.10.1    | same month, so MICRO goes up            |
      | YYYY.0M.MICRO    | 2026-10-02 | 2026.09.4    | 2026.10.0    | a new month starts MICRO again          |
      | YYYY.0M.MICRO    | 2027-01-05 | 2026.12.2    | 2027.01.0    | a new year starts MICRO again           |
      | YY.MM.MICRO      | 2026-10-02 | 26.9.0       | 26.10.0      | short year and month without padding    |
      | YYYY.0M.0D       | 2026-10-02 | 2026.10.01   | 2026.10.02   | one release a day                       |
      | YYYY.0W.MICRO    | 2026-10-02 | 2026.39.0    | 2026.40.0    | ISO week numbers                        |
      | YYYY.0M.0D.MICRO | 2026-10-02 | 2026.10.02.0 | 2026.10.02.1 | several releases on the same day        |

  Scenario: The first calendar release is today's version
    Given today is 2026-10-02
    And the release config:
      """toml
      [packages.site]
      path = "."
      scheme = "calver"
      """
    And the git history:
      """
      A  feat: first page
         changes  index.html
      """
    When release-bot runs
    Then the release branch releases:
      | package | from       | to        |
      | site    | unreleased | 2026.10.0 |

  Scenario: A format without MICRO can't release twice for the same date
    Given today is 2026-10-02
    And the release config:
      """toml
      [packages.site]
      path = "."
      scheme = "calver"
      calver-format = "YYYY.0M.0D"
      """
    And the git history:
      """
      A  chore: release 2026.10.02
         manifest  site 2026.10.02
         tags      v2026.10.02

      B  fix: correct a broken link
         changes  index.html
      """
    When release-bot runs
    Then release-bot fails, saying "2026.10.02 was already released for this date"
    And there is no release branch
