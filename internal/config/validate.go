package config

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)

func (c *Config) validate() error {
	if len(c.Packages) == 0 {
		return fmt.Errorf("%w: no packages configured", ErrInvalid)
	}
	if len(c.Released()) == 0 {
		return fmt.Errorf("%w: every package has release = false, so nothing would ever be released", ErrInvalid)
	}
	if err := validateRefName("branch", c.Branch); err != nil {
		return err
	}

	paths := map[string]string{}
	tags := map[string]string{}
	changelogs := map[string]string{}
	var errs []error
	for i := range c.Packages {
		pkg := &c.Packages[i]
		errs = append(errs, c.validatePackage(pkg), claim(paths, pkg.Path, pkg.Name, "path"))
		if pkg.Release {
			errs = append(errs,
				claim(tags, pkg.TagFor("{version}"), pkg.Name, "tag"),
				claim(changelogs, pkg.Changelog, pkg.Name, "changelog"))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	return c.checkCycles()
}

func (c *Config) validatePackage(pkg *Package) error {
	field := func(name string) string { return "packages." + pkg.Name + "." + name }
	var errs []error
	if !namePattern.MatchString(pkg.Name) {
		errs = append(errs, fmt.Errorf("%w: package name %q must be letters, digits, '.', '_', '-' or '/'",
			ErrInvalid, pkg.Name))
	}
	if !strings.Contains(pkg.Tag, "{version}") {
		errs = append(errs, fmt.Errorf("%w: %s %q must contain {version}", ErrInvalid, field("tag"), pkg.Tag))
	} else {
		errs = append(errs, validateRefName(field("tag"), pkg.TagFor("1.0.0")))
	}
	for _, dep := range pkg.DependsOn {
		if dep == pkg.Name {
			errs = append(errs, fmt.Errorf("%w: %s lists the package itself", ErrInvalid, field("depends-on")))
		} else if c.Package(dep) == nil {
			errs = append(errs, fmt.Errorf("%w: %s: unknown package %q", ErrInvalid, field("depends-on"), dep))
		}
	}
	for _, glob := range append(append([]string{}, pkg.Also...), pkg.Exclude...) {
		if !doublestar.ValidatePattern(glob) {
			errs = append(errs, fmt.Errorf("%w: %s: bad glob %q", ErrInvalid, pkg.Name, glob))
		}
	}
	return errors.Join(errs...)
}

// claim records that owner uses value, failing if another package already does.
func claim(used map[string]string, value, owner, what string) error {
	if other, ok := used[value]; ok {
		return fmt.Errorf("%w: packages %s and %s have the same %s %q", ErrInvalid, other, owner, what, value)
	}
	used[value] = owner
	return nil
}

// checkCycles rejects depends-on cycles, which would make propagation never settle.
func (c *Config) checkCycles() error {
	const (
		unvisited = iota
		visiting
		done
	)
	state := map[string]int{}
	var visit func(name string, trail []string) error
	visit = func(name string, trail []string) error {
		switch state[name] {
		case visiting:
			return fmt.Errorf("%w: depends-on cycle %s", ErrInvalid, strings.Join(append(trail, name), " -> "))
		case done:
			return nil
		}
		state[name] = visiting
		for _, dep := range c.Package(name).DependsOn {
			if err := visit(dep, append(trail, name)); err != nil {
				return err
			}
		}
		state[name] = done
		return nil
	}
	for i := range c.Packages {
		if name := c.Packages[i].Name; state[name] == unvisited {
			if err := visit(name, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

// refRules are the subset of git check-ref-format rules that a template can break.
var refRules = []struct {
	reason string
	broken func(ref string) bool
}{
	{"is empty", func(ref string) bool { return ref == "" }},
	{"contains a space or one of ~^:?*[\\", func(ref string) bool { return strings.ContainsAny(ref, " ~^:?*[\\") }},
	{"contains ..", func(ref string) bool { return strings.Contains(ref, "..") }},
	{"contains @{", func(ref string) bool { return strings.Contains(ref, "@{") }},
	{"starts with /", func(ref string) bool { return strings.HasPrefix(ref, "/") }},
	{"ends with /", func(ref string) bool { return strings.HasSuffix(ref, "/") }},
	{"contains //", func(ref string) bool { return strings.Contains(ref, "//") }},
	{"has a part starting with .", func(ref string) bool {
		return strings.HasPrefix(ref, ".") || strings.Contains(ref, "/.")
	}},
	{"ends with .lock", func(ref string) bool { return strings.HasSuffix(ref, ".lock") }},
	{"ends with .", func(ref string) bool { return strings.HasSuffix(ref, ".") }},
}

func validateRefName(field, ref string) error {
	for _, rule := range refRules {
		if rule.broken(ref) {
			return fmt.Errorf("%w: %s %q is not a valid git ref name: it %s", ErrInvalid, field, ref, rule.reason)
		}
	}
	return nil
}
