// Package vcs holds the types that version control adapters return, so adapters don't depend on the planner.
package vcs

// Commit is a commit and the files it changed.
type Commit struct {
	SHA     string
	Message string
	// Files are the paths the commit changed, relative to the repository root.
	Files []string
}
