// Package manifest reads and writes the versions manifest, which maps each released package to its current version.
package manifest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
)

// ErrInvalid is returned for manifests that aren't a JSON object of strings.
var ErrInvalid = errors.New("invalid manifest")

// Manifest maps package names to their current released version.
type Manifest map[string]string

// Parse parses a manifest. Empty input is an empty manifest.
func Parse(data []byte) (Manifest, error) {
	versions := Manifest{}
	if len(bytes.TrimSpace(data)) == 0 {
		return versions, nil
	}
	if err := json.Unmarshal(data, &versions); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return versions, nil
}

// Marshal renders the manifest as indented JSON with sorted keys and a trailing newline.
func (m Manifest) Marshal() ([]byte, error) {
	if m == nil {
		m = Manifest{}
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return append(data, '\n'), nil
}

// With returns a copy of the manifest with the given versions set.
func (m Manifest) With(versions map[string]string) Manifest {
	updated := make(Manifest, len(m)+len(versions))
	maps.Copy(updated, m)
	maps.Copy(updated, versions)
	return updated
}
