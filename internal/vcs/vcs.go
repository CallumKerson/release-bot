// Package vcs holds the types that version control adapters return, so adapters don't depend on the planner.
package vcs

// Commit is a commit and the files it changed.
type Commit struct {
	SHA     string
	Message string
	// Files are the paths the commit changed, relative to the repository root.
	Files []string
}

// Short abbreviates a commit ID to the 7 characters git shows by default.
func Short(sha string) string {
	return sha[:min(len(sha), 7)]
}
