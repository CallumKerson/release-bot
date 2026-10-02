// Package config loads and validates release-bot.toml.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/pelletier/go-toml/v2"

	"github.com/CallumKerson/release-bot/internal/version"
)

const (
	DefaultBranch   = "release-bot/release"
	DefaultManifest = ".release-bot-manifest.json"

	defaultTag     = "{name}-v{version}"
	defaultRootTag = "v{version}"
	changelogName  = "CHANGELOG.md"
)

// Paths searched for the config file, relative to the repository root.
var searchPaths = []string{"release-bot.toml", filepath.Join(".config", "release-bot.toml")}

var (
	// ErrInvalid is returned for configs that parse but don't make sense.
	ErrInvalid = errors.New("invalid config")
	// ErrNotFound is returned when the repository has no config file.
	ErrNotFound = errors.New("no config found")

	errRequired    = errors.New("required")
	errOutsideRepo = errors.New("must be inside the repository")
)

// Config is a validated release-bot configuration with package defaults applied.
type Config struct {
	// Branch is the local release branch rebuilt on every run.
	Branch string
	// Manifest is the repository path of the versions manifest.
	Manifest string
	// Bumps overrides how far each conventional commit type bumps a version.
	Bumps map[string]version.Bump
	// Packages are sorted by name.
	Packages []Package
}

// Package is a directory of the repository tracked by release-bot.
type Package struct {
	Name string
	// Path is a clean, slash-separated path relative to the repository root; "." is the root.
	Path string
	// Release is false for shared code that is tracked so its changes reach dependents, but is never tagged.
	Release        bool
	Scheme         string
	CalverFormat   string
	InitialVersion string
	// Tag is a template with {name}, {path} and {version} tokens.
	Tag       string
	Changelog string
	DependsOn []string
	Also      []string
	Exclude   []string
}

type fileConfig struct {
	Branch   string                  `toml:"branch"`
	Manifest string                  `toml:"manifest"`
	Defaults fileDefaults            `toml:"defaults"`
	Bump     map[string]string       `toml:"bump"`
	Packages map[string]*filePackage `toml:"packages"`
}

type fileDefaults struct {
	Scheme         string `toml:"scheme"`
	CalverFormat   string `toml:"calver-format"`
	InitialVersion string `toml:"initial-version"`
	Tag            string `toml:"tag"`
}

type filePackage struct {
	fileDefaults

	Path      string   `toml:"path"`
	Release   *bool    `toml:"release"`
	Changelog string   `toml:"changelog"`
	DependsOn []string `toml:"depends-on"`
	Also      []string `toml:"also"`
	Exclude   []string `toml:"exclude"`
}

// Find returns the path of the config file under root, or explicit when it is set.
func Find(root, explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	for _, candidate := range searchPaths {
		full := filepath.Join(root, candidate)
		if _, err := os.Stat(full); err == nil {
			return full, nil
		}
	}
	return "", fmt.Errorf("%w in %s: create one of %s", ErrNotFound, root, strings.Join(searchPaths, " or "))
}

// Load reads and parses the config file at file.
func Load(file string) (Config, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return Config{}, fmt.Errorf("reading config: %w", err)
	}
	cfg, err := Parse(data)
	if err != nil {
		return Config{}, fmt.Errorf("%s: %w", file, err)
	}
	return cfg, nil
}

// Parse parses and validates a TOML config.
func Parse(data []byte) (Config, error) {
	var raw fileConfig
	decoder := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields()
	if err := decoder.Decode(&raw); err != nil {
		if strict, ok := errors.AsType[*toml.StrictMissingError](err); ok {
			return Config{}, fmt.Errorf("%w: %s", ErrInvalid, strict.String())
		}
		return Config{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	cfg, err := resolve(&raw)
	if err != nil {
		return Config{}, err
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func resolve(raw *fileConfig) (Config, error) {
	cfg := Config{
		Branch:   orDefault(raw.Branch, DefaultBranch),
		Manifest: orDefault(raw.Manifest, DefaultManifest),
		Bumps:    map[string]version.Bump{},
	}
	for commitType, name := range raw.Bump {
		bump, err := version.ParseBump(name)
		if err != nil {
			return Config{}, fmt.Errorf("%w: bump.%s: %w", ErrInvalid, commitType, err)
		}
		cfg.Bumps[strings.ToLower(commitType)] = bump
	}

	for name, rawPkg := range raw.Packages {
		pkgPath, err := cleanPath(rawPkg.Path)
		if err != nil {
			return Config{}, fmt.Errorf("%w: packages.%s.path: %w", ErrInvalid, name, err)
		}
		pkg := Package{
			Name:           name,
			Path:           pkgPath,
			Release:        rawPkg.Release == nil || *rawPkg.Release,
			Scheme:         firstSet(rawPkg.Scheme, raw.Defaults.Scheme, version.SchemeSemver),
			CalverFormat:   firstSet(rawPkg.CalverFormat, raw.Defaults.CalverFormat),
			InitialVersion: firstSet(rawPkg.InitialVersion, raw.Defaults.InitialVersion),
			Tag:            firstSet(rawPkg.Tag, raw.Defaults.Tag, defaultTagFor(pkgPath)),
			Changelog:      firstSet(rawPkg.Changelog, path.Join(pkgPath, changelogName)),
			DependsOn:      rawPkg.DependsOn,
			Also:           rawPkg.Also,
			Exclude:        rawPkg.Exclude,
		}
		cfg.Packages = append(cfg.Packages, pkg)
	}
	slices.SortFunc(cfg.Packages, func(a, b Package) int { return strings.Compare(a.Name, b.Name) })
	return cfg, nil
}

func defaultTagFor(pkgPath string) string {
	if pkgPath == "." {
		return defaultRootTag
	}
	return defaultTag
}

// cleanPath normalises a package path to a slash-separated path relative to the repository root.
func cleanPath(raw string) (string, error) {
	if raw == "" {
		return "", errRequired
	}
	cleaned := path.Clean(strings.ReplaceAll(raw, `\`, "/"))
	if path.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("%q %w", raw, errOutsideRepo)
	}
	return cleaned, nil
}

func orDefault(value, fallback string) string {
	return firstSet(value, fallback)
}

func firstSet(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// Package returns the package with the given name, or nil if there is none.
func (c *Config) Package(name string) *Package {
	for i := range c.Packages {
		if c.Packages[i].Name == name {
			return &c.Packages[i]
		}
	}
	return nil
}

// Released returns the packages that are tagged and versioned, sorted by name.
func (c *Config) Released() []*Package {
	var released []*Package
	for i := range c.Packages {
		if c.Packages[i].Release {
			released = append(released, &c.Packages[i])
		}
	}
	return released
}

// VersionScheme returns the package's versioning scheme.
func (p *Package) VersionScheme() (version.Scheme, error) {
	return version.New(p.Scheme, p.CalverFormat, p.InitialVersion)
}

// TagFor renders the package's tag for a version.
// For the root package "{path}/" renders as nothing, so "{path}/v{version}" gives "v1.2.3".
func (p *Package) TagFor(ver string) string {
	tag := p.Tag
	if p.Path == "." {
		tag = strings.ReplaceAll(tag, "{path}/", "")
	}
	return strings.NewReplacer("{name}", p.Name, "{path}", p.Path, "{version}", ver).Replace(tag)
}

// Contains reports whether file is under the package's path.
// It doesn't consider excludes, or other packages nested inside this one.
func (p *Package) Contains(file string) bool {
	return p.Path == "." || file == p.Path || strings.HasPrefix(file, p.Path+"/")
}

// Excludes reports whether one of the package's exclude globs matches file.
func (p *Package) Excludes(file string) bool {
	return matchesAny(p.Exclude, file)
}

// AlsoMatch returns the first of the package's also globs that matches file.
func (p *Package) AlsoMatch(file string) (string, bool) {
	for _, glob := range p.Also {
		if ok, _ := doublestar.Match(glob, file); ok {
			return glob, true
		}
	}
	return "", false
}

func matchesAny(globs []string, file string) bool {
	for _, glob := range globs {
		if ok, _ := doublestar.Match(glob, file); ok {
			return true
		}
	}
	return false
}
