package planner

import (
	"github.com/CallumKerson/release-bot/internal/config"
)

// ReasonKind is the rule that made a commit relevant to a package.
type ReasonKind string

const (
	// ByPath means the commit changed a file the package owns.
	ByPath ReasonKind = "path"
	// ByDependency means the commit changed a file owned by a package in the package's depends-on closure.
	ByDependency ReasonKind = "depends-on"
	// ByAlso means the commit changed a file matching one of the also globs of the package or its dependencies.
	ByAlso ReasonKind = "also"
	// ByEmptyCommit means the commit changed no files, so it is about the whole repository and counts for the root package.
	// This is how a Release-As footer is usually added: git commit --allow-empty.
	ByEmptyCommit ReasonKind = "empty"
)

// Reason explains why a commit counts toward a package.
type Reason struct {
	Kind ReasonKind `json:"kind"`
	// Via is the dependency for ByDependency, or the glob for ByAlso.
	Via string `json:"via,omitempty"`
	// File is the first changed file that matched. It is empty for ByEmptyCommit.
	File string `json:"file,omitempty"`
}

// owner returns the package with the longest path containing file, or nil if no package owns it.
// A file excluded by that package belongs to no package, rather than falling through to an enclosing one.
func owner(cfg *config.Config, file string) *config.Package {
	var best *config.Package
	for i := range cfg.Packages {
		pkg := &cfg.Packages[i]
		if pkg.Contains(file) && (best == nil || depth(pkg.Path) > depth(best.Path)) {
			best = pkg
		}
	}
	if best == nil || best.Excludes(file) {
		return nil
	}
	return best
}

// depth orders paths so that nested packages win over the packages that enclose them.
// Package paths that both contain a file are prefixes of each other, so length is enough.
func depth(pkgPath string) int {
	if pkgPath == "." {
		return 0
	}
	return len(pkgPath)
}

// closure returns pkg followed by every package it transitively depends on, each once.
// The config has been validated, so every dependency exists and there are no cycles.
func closure(cfg *config.Config, pkg *config.Package) []*config.Package {
	seen := map[string]bool{}
	var out []*config.Package
	var visit func(current *config.Package)
	visit = func(current *config.Package) {
		if seen[current.Name] {
			return
		}
		seen[current.Name] = true
		out = append(out, current)
		for _, dep := range current.DependsOn {
			if found := cfg.Package(dep); found != nil {
				visit(found)
			}
		}
	}
	visit(pkg)
	return out
}

// relevance returns why commit counts toward the first package in pkgs, which is the package's closure.
// It returns nothing when the commit doesn't affect the package. Each rule is reported once, with its first file.
func relevance(cfg *config.Config, pkgs []*config.Package, commit *Commit) []Reason {
	if len(commit.Files) == 0 {
		if pkgs[0].Path == "." {
			return []Reason{{Kind: ByEmptyCommit}}
		}
		return nil
	}

	self := pkgs[0].Name
	inClosure := map[string]bool{}
	for _, pkg := range pkgs {
		inClosure[pkg.Name] = true
	}

	var reasons []Reason
	seen := map[Reason]bool{}
	add := func(reason Reason) {
		key := Reason{Kind: reason.Kind, Via: reason.Via}
		if !seen[key] {
			seen[key] = true
			reasons = append(reasons, reason)
		}
	}
	for _, file := range commit.Files {
		if own := owner(cfg, file); own != nil && inClosure[own.Name] {
			if own.Name == self {
				add(Reason{Kind: ByPath, File: file})
			} else {
				add(Reason{Kind: ByDependency, Via: own.Name, File: file})
			}
		}
		for _, pkg := range pkgs {
			if glob, ok := pkg.AlsoMatch(file); ok {
				add(Reason{Kind: ByAlso, Via: glob, File: file})
			}
		}
	}
	return reasons
}
