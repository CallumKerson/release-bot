// Package gitrepo builds throwaway git repositories for tests, isolated from the user's git config.
package gitrepo

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// isolatedConfig replaces the user's global git config, so signing, hooks and aliases can't affect tests.
// Automatic maintenance is off, so no git process left running in the background after a commit or push
// writes into a repository while the test is removing it.
const isolatedConfig = `[user]
	name = Test User
	email = test@example.com
[init]
	defaultBranch = main
[commit]
	gpgSign = false
[tag]
	gpgSign = false
[merge]
	ff = true
[maintenance]
	auto = false
[gc]
	auto = 0
[receive]
	autogc = false
`

// Repo is a temporary git repository.
type Repo struct {
	t   testing.TB
	Dir string
}

// Isolate points git at an isolated global config for the rest of the test, so the test can't run in parallel.
func Isolate(tb testing.TB) {
	tb.Helper()
	home := tb.TempDir()
	globalConfig := filepath.Join(home, ".gitconfig")
	if err := os.WriteFile(globalConfig, []byte(isolatedConfig), 0o600); err != nil {
		tb.Fatal(err)
	}
	tb.Setenv("HOME", home)
	tb.Setenv("XDG_CONFIG_HOME", home)
	tb.Setenv("GIT_CONFIG_GLOBAL", globalConfig)
	tb.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

// New creates an empty repository on branch main, isolated from the user's git config.
func New(t testing.TB) *Repo {
	t.Helper()
	Isolate(t)
	repo := &Repo{t: t, Dir: t.TempDir()}
	repo.Git("init", "--quiet")
	return repo
}

// Git runs git in the repository and returns its trimmed output, failing the test on error.
func (r *Repo) Git(args ...string) string {
	r.t.Helper()
	cmd := exec.CommandContext(r.t.Context(), "git", args...)
	cmd.Dir = r.Dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// Write writes a file relative to the repository root, creating directories as needed.
func (r *Repo) Write(path, content string) {
	r.t.Helper()
	full := filepath.Join(r.Dir, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		r.t.Fatal(err)
	}
}

// Read reads a file relative to the repository root from the working tree.
func (r *Repo) Read(path string) string {
	r.t.Helper()
	data, err := os.ReadFile(filepath.Join(r.Dir, filepath.FromSlash(path)))
	if err != nil {
		r.t.Fatal(err)
	}
	return string(data)
}

// Commit writes files, stages everything and commits with message, returning the new commit.
// Each file's content is its path plus the message, so repeated commits to a file always change it.
func (r *Repo) Commit(message string, files ...string) string {
	r.t.Helper()
	for _, file := range files {
		r.Write(file, file+"\n"+message+"\n")
	}
	r.Git("add", "--all")
	r.Git("commit", "--quiet", "--allow-empty", "--message", message)
	return r.Head()
}

// Head returns the commit HEAD points to.
func (r *Repo) Head() string {
	r.t.Helper()
	return r.Git("rev-parse", "HEAD")
}

// Status returns the porcelain status, which is empty for a clean working tree.
func (r *Repo) Status() string {
	r.t.Helper()
	return r.Git("status", "--porcelain")
}
