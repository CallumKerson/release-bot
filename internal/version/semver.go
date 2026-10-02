package version

import (
	"cmp"
	"fmt"
	"regexp"
	"strconv"
	"time"
)

// DefaultSemverInitial is the first release of a semver package unless configured otherwise.
const DefaultSemverInitial = "0.1.0"

var semverPattern = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$`)

type semver struct {
	initial string
}

// NewSemver returns a semantic versioning scheme whose first release is initial,
// or DefaultSemverInitial when initial is empty.
//
// Below 1.0.0 a breaking change bumps the minor version rather than the major,
// so a package only reaches 1.0.0 when someone decides it should.
func NewSemver(initial string) (Scheme, error) {
	if initial == "" {
		initial = DefaultSemverInitial
	}
	if _, err := parseSemver(initial); err != nil {
		return nil, fmt.Errorf("initial version: %w", err)
	}
	return semver{initial: initial}, nil
}

func (s semver) Initial(time.Time) (string, error) {
	return s.initial, nil
}

func (semver) Validate(v string) error {
	_, err := parseSemver(v)
	return err
}

func (semver) Next(current string, bump Bump, _ time.Time) (string, error) {
	parts, err := parseSemver(current)
	if err != nil {
		return "", err
	}
	major, minor, patch := parts[0], parts[1], parts[2]

	if bump == Major && major == 0 {
		bump = Minor
	}
	switch bump {
	case Major:
		major, minor, patch = major+1, 0, 0
	case Minor:
		minor, patch = minor+1, 0
	case Patch:
		patch++
	case None:
		return current, nil
	}
	return fmt.Sprintf("%d.%d.%d", major, minor, patch), nil
}

func (semver) Compare(a, b string) (int, error) {
	partsA, err := parseSemver(a)
	if err != nil {
		return 0, err
	}
	partsB, err := parseSemver(b)
	if err != nil {
		return 0, err
	}
	for i := range partsA {
		if c := cmp.Compare(partsA[i], partsB[i]); c != 0 {
			return c, nil
		}
	}
	return 0, nil
}

func parseSemver(ver string) ([3]int, error) {
	match := semverPattern.FindStringSubmatch(ver)
	if match == nil {
		return [3]int{}, fmt.Errorf("%w: %q is not a MAJOR.MINOR.PATCH semantic version", ErrInvalid, ver)
	}
	var parts [3]int
	for i := range parts {
		n, err := strconv.Atoi(match[i+1])
		if err != nil {
			return [3]int{}, fmt.Errorf("%w: %q: %w", ErrInvalid, ver, err)
		}
		parts[i] = n
	}
	return parts, nil
}
