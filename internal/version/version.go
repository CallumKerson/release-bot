// Package version computes the next version of a package under a versioning scheme.
package version

import (
	"errors"
	"fmt"
	"time"
)

// Bump is how far a set of changes moves a version.
type Bump int

const (
	None Bump = iota
	Patch
	Minor
	Major
)

var bumpNames = map[Bump]string{None: "none", Patch: "patch", Minor: "minor", Major: "major"}

func (b Bump) String() string {
	if name, ok := bumpNames[b]; ok {
		return name
	}
	return fmt.Sprintf("Bump(%d)", int(b))
}

// MarshalText renders the bump by name, so plans read naturally as JSON.
func (b Bump) MarshalText() ([]byte, error) {
	return []byte(b.String()), nil
}

// ParseBump parses a bump name such as "minor".
func ParseBump(name string) (Bump, error) {
	for bump, bumpName := range bumpNames {
		if bumpName == name {
			return bump, nil
		}
	}
	return None, fmt.Errorf("%w: unknown bump %q, want one of none, patch, minor or major", ErrInvalid, name)
}

// Scheme is a versioning scheme, such as semantic or calendar versioning.
type Scheme interface {
	// Initial is the version of a package's first release.
	Initial(now time.Time) (string, error)
	// Next is the version after current, for changes of the given bump.
	Next(current string, bump Bump, now time.Time) (string, error)
	// Validate reports whether v is a well-formed version in this scheme.
	Validate(v string) error
	// Compare returns -1, 0 or +1 as a is older than, the same as, or newer than b.
	Compare(a, b string) (int, error)
}

// ErrInvalid is returned for malformed versions, formats and scheme names.
var ErrInvalid = errors.New("invalid version")

// Names of the schemes, as the config gives them.
const (
	SchemeSemver = "semver"
	SchemeCalver = "calver"
)

// New returns the scheme with the given name.
// calverFormat is only used by calver, and initial only by semver.
func New(name, calverFormat, initial string) (Scheme, error) {
	switch name {
	case SchemeSemver:
		return NewSemver(initial)
	case SchemeCalver:
		return NewCalver(calverFormat)
	default:
		return nil, fmt.Errorf("%w: unknown scheme %q, want %q or %q", ErrInvalid, name, SchemeSemver, SchemeCalver)
	}
}
