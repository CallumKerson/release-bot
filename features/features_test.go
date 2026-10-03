package features

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/CallumKerson/release-bot/internal/cli"
	"github.com/CallumKerson/release-bot/internal/config"
	"github.com/CallumKerson/release-bot/internal/manifest"
	"github.com/CallumKerson/release-bot/internal/testing/gitrepo"
	"github.com/CallumKerson/release-bot/internal/vcs"
)

// These steps run each scenario against a fresh git repository, through the real commands.
// See README.md for how to run them.

var godogOptions = godog.Options{Format: "progress", Paths: []string{"."}, Strict: true}

func init() { //nolint:gochecknoinits // godog flags must be registered before flag.Parse
	godog.BindFlags("godog.", flag.CommandLine, &godogOptions)
}

func TestFeatures(t *testing.T) {
	gitrepo.Isolate(t)
	opts := godogOptions
	opts.TestingT = t
	suite := godog.TestSuite{Name: "release-bot", ScenarioInitializer: initializeScenario, Options: &opts}
	if suite.Run() != 0 {
		t.Fatal("feature scenarios failed")
	}
}

// releaseLabel is the label of the commit release-bot puts on the release branch.
const releaseLabel = "release"

var (
	// errNotAsExpected fails a Then step, phrased as "<what> is not as expected: <how>".
	errNotAsExpected = errors.New("not as expected")
	// errScenario fails a Given step that the scenario wrote wrongly.
	errScenario = errors.New("the scenario is set up wrong")
)

// world is the state of one scenario.
type world struct {
	dir   string
	today time.Time
	cfg   config.Config

	// labels maps the names used in the feature to commits, and shas maps them back for readable output.
	labels map[string]string
	shas   map[string]string

	// env is the environment release-bot sees.
	env map[string]string
	// github is the scenario's fake GitHub, once the repository is on it.
	github *gitHub

	// before is the repository as it was before the last command ran.
	before snapshot
	output string
	err    error
}

type snapshot struct {
	head, status, refs string
	tags               []string
}

func initializeScenario(scenario *godog.ScenarioContext) {
	state := &world{labels: map[string]string{}, shas: map[string]string{}, env: map[string]string{}}
	scenario.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		dir, err := os.MkdirTemp("", "release-bot-feature-")
		if err != nil {
			return ctx, err
		}
		state.dir = dir
		state.cfg, _ = config.Parse([]byte("[packages.root]\npath = \".\""))
		_, err = state.git(ctx, nil, "init", "--quiet")
		return ctx, err
	})
	scenario.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		return ctx, errors.Join(err, os.RemoveAll(state.dir))
	})

	scenario.Step(`^today is (\d{4}-\d{2}-\d{2})$`, state.todayIs)
	scenario.Step(`^the release config:$`, state.theReleaseConfig)
	scenario.Step(`^the git history:$`, state.theGitHistory)
	scenario.Step(`^the git history continues:$`, state.theGitHistory)
	scenario.Step(`^the branch "([^"]+)" starts from (\S+) with the history:$`, state.aBranchWithHistory)
	scenario.Step(`^"([^"]+)" is merged into main with a merge commit$`, state.mergedWithMergeCommit)
	scenario.Step(`^someone has uncommitted work in progress$`, state.uncommittedWork)

	scenario.Step(`^release-bot (runs|plans)$`, state.releaseBotRuns)
	scenario.Step(`^release-bot runs with (--[\w-]+(?: --[\w-]+)*)$`, state.releaseBotRunsWith)
	scenario.Step(
		`^the release branch is merged with (a fast-forward|a merge commit|a squash merge)$`,
		state.releaseMerged,
	)

	scenario.Step(`^release-bot says:$`, state.releaseBotSays)
	scenario.Step(`^release-bot fails, saying "(.*)"$`, state.releaseBotFails)
	scenario.Step(`^the release branch releases:$`, state.theReleaseBranchReleases)
	scenario.Step(`^there is no release branch$`, state.thereIsNoReleaseBranch)
	scenario.Step(`^the release branch is one commit on top of main$`, state.oneCommitOnTopOfMain)
	scenario.Step(`^the release branch changes "([^"]+)" to:$`, state.theReleaseBranchChanges)
	scenario.Step(`^the release commit message is:$`, state.theReleaseCommitMessage)
	scenario.Step(`^these tags are created:$`, state.theseTagsAreCreated)
	scenario.Step(`^no tags are created$`, state.noTagsAreCreated)
	scenario.Step(`^the working tree is untouched$`, state.theWorkingTreeIsUntouched)
	scenario.Step(`^nothing in the repository changes$`, state.nothingChanges)

	registerGitHubSteps(scenario, state)
}

// Given

func (w *world) todayIs(day string) error {
	today, err := time.Parse(time.DateOnly, day)
	w.today = today
	return err
}

// theReleaseConfig writes release-bot.toml. It is committed with the first commit of the history.
func (w *world) theReleaseConfig(content *godog.DocString) error {
	if cfg, err := config.Parse([]byte(content.Content)); err == nil {
		w.cfg = cfg
	}
	return os.WriteFile(filepath.Join(w.dir, "release-bot.toml"), []byte(content.Content+"\n"), 0o600)
}

// theGitHistory makes the commits of history. When the repository is on GitHub, main is pushed there too.
func (w *world) theGitHistory(ctx context.Context, history *godog.DocString) error {
	commits, err := parseHistory(history.Content)
	if err != nil {
		return err
	}
	for i := range commits {
		commit := &commits[i]
		if err := w.commit(ctx, commit); err != nil {
			return fmt.Errorf("commit %s: %w", commit.label, err)
		}
	}
	return w.push(ctx)
}

func (w *world) commit(ctx context.Context, commit *historyCommit) error {
	if _, taken := w.labels[commit.label]; taken {
		return fmt.Errorf("%w: the label %s is already used", errScenario, commit.label)
	}
	for _, file := range commit.changes {
		if err := w.write(file, file+" as of "+commit.label+"\n"); err != nil {
			return err
		}
	}
	for _, file := range commit.deletes {
		if _, err := w.git(ctx, nil, "rm", "--quiet", file); err != nil {
			return err
		}
	}
	if commit.manifest != nil {
		data, err := manifest.Manifest(commit.manifest).Marshal()
		if err != nil {
			return err
		}
		if err := w.write(w.cfg.Manifest, string(data)); err != nil {
			return err
		}
	}
	if _, err := w.git(ctx, nil, "add", "--all"); err != nil {
		return err
	}
	if _, err := w.git(
		ctx,
		strings.NewReader(commit.message()),
		"commit",
		"--quiet",
		"--allow-empty",
		"--file",
		"-",
	); err != nil {
		return err
	}
	sha, err := w.git(ctx, nil, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	w.label(commit.label, sha)
	for _, tag := range commit.tags {
		if _, err := w.git(ctx, nil, "tag", "--annotate", "--message", tag, tag); err != nil {
			return err
		}
	}
	return nil
}

func (w *world) aBranchWithHistory(ctx context.Context, branch, from string, history *godog.DocString) error {
	start, ok := w.labels[from]
	if !ok {
		return fmt.Errorf("%w: no commit is labelled %s", errScenario, from)
	}
	if _, err := w.git(ctx, nil, "switch", "--quiet", "--create", branch, start); err != nil {
		return err
	}
	if err := w.theGitHistory(ctx, history); err != nil {
		return err
	}
	_, err := w.git(ctx, nil, "switch", "--quiet", "main")
	return err
}

func (w *world) mergedWithMergeCommit(ctx context.Context, branch string) error {
	if _, err := w.git(ctx, nil, "merge", "--quiet", "--no-ff", "--no-edit", branch); err != nil {
		return err
	}
	return w.labelHead(ctx, "merge of "+branch)
}

func (w *world) uncommittedWork() error {
	return w.write("work-in-progress.txt", "not committed yet\n")
}

// When

func (w *world) releaseBotRuns(ctx context.Context, command string) error {
	return w.releaseBot(ctx, strings.TrimSuffix(command, "s"))
}

func (w *world) releaseBotRunsWith(ctx context.Context, options string) error {
	return w.releaseBot(ctx, append([]string{"run"}, strings.Fields(options)...)...)
}

// releaseBot runs a release-bot command in the scenario's repository, recording its output.
// The release branch commit it makes is labelled "release".
func (w *world) releaseBot(ctx context.Context, args ...string) error {
	var err error
	if w.before, err = w.snapshot(ctx); err != nil {
		return err
	}
	if w.github != nil {
		if w.github.before, err = w.gitHubSnapshot(ctx); err != nil {
			return err
		}
	}
	cmd := cli.NewRootCommand(func() time.Time { return w.today }, w.getenv)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(append([]string{"--repo", w.dir}, args...))
	w.err = cmd.ExecuteContext(ctx)
	w.output = out.String()

	if sha, err := w.git(ctx, nil, "rev-parse", "--verify", "--quiet", w.cfg.Branch); err == nil {
		if _, labelled := w.shas[sha]; !labelled {
			w.label(releaseLabel, sha)
		}
	}
	return nil
}

func (w *world) releaseMerged(ctx context.Context, strategy string) error {
	branch := w.cfg.Branch
	switch strategy {
	case "a fast-forward":
		_, err := w.git(ctx, nil, "merge", "--quiet", "--ff-only", branch)
		return err
	case "a merge commit":
		return w.mergedWithMergeCommit(ctx, branch)
	default:
		// Like GitHub, the squash commit's title is the pull request's title and number.
		message, err := w.git(ctx, nil, "log", "-1", "--format=%s (#1)%n%n%b", branch)
		if err != nil {
			return err
		}
		if _, err := w.git(ctx, nil, "merge", "--quiet", "--squash", branch); err != nil {
			return err
		}
		if _, err := w.git(ctx, strings.NewReader(message), "commit", "--quiet", "--file", "-"); err != nil {
			return err
		}
		return w.labelHead(ctx, "squashed release")
	}
}

// Then

func (w *world) releaseBotSays(expected *godog.DocString) error {
	if w.err != nil {
		return fmt.Errorf("release-bot failed: %w\n%s", w.err, w.output)
	}
	return compare("release-bot's output", expected.Content, w.readable(w.output))
}

// releaseBotFails checks the error contains message. Quotes inside the message are written \" in the feature.
func (w *world) releaseBotFails(message string) error {
	message = strings.ReplaceAll(message, `\"`, `"`)
	if w.err == nil {
		return fmt.Errorf("release-bot's result is %w: it succeeded, saying:\n%s", errNotAsExpected, w.output)
	}
	if got := w.readable(w.err.Error()); !strings.Contains(got, message) {
		return fmt.Errorf("release-bot's error is %w: it said %q", errNotAsExpected, got)
	}
	return nil
}

func (w *world) theReleaseBranchReleases(ctx context.Context, expected *godog.Table) error {
	if err := w.thereIsAReleaseBranch(ctx); err != nil {
		return err
	}
	before, err := w.manifestAt(ctx, "main")
	if err != nil {
		return err
	}
	after, err := w.manifestAt(ctx, w.cfg.Branch)
	if err != nil {
		return err
	}
	var got []string
	for name, next := range after {
		if before[name] != next {
			got = append(got, row(name, orUnreleased(before[name]), next))
		}
	}
	var want []string
	for _, r := range expected.Rows[1:] {
		want = append(want, row(r.Cells[0].Value, r.Cells[1].Value, r.Cells[2].Value))
	}
	slices.Sort(got)
	slices.Sort(want)
	return compare("releases on the release branch", strings.Join(want, "\n"), strings.Join(got, "\n"))
}

func (w *world) thereIsNoReleaseBranch(ctx context.Context) error {
	if sha, err := w.git(ctx, nil, "rev-parse", "--verify", "--quiet", w.cfg.Branch); err == nil {
		return fmt.Errorf("the release branch is %w: it exists, at %s", errNotAsExpected, w.readable(sha))
	}
	return nil
}

func (w *world) oneCommitOnTopOfMain(ctx context.Context) error {
	parent, err := w.git(ctx, nil, "rev-parse", w.cfg.Branch+"^")
	if err != nil {
		return err
	}
	head, err := w.git(ctx, nil, "rev-parse", "main")
	if err != nil {
		return err
	}
	if parent != head {
		return fmt.Errorf("the release branch is %w: it is on top of %s, not main (%s)",
			errNotAsExpected, w.readable(parent), w.readable(head))
	}
	return nil
}

func (w *world) theReleaseBranchChanges(ctx context.Context, path string, expected *godog.DocString) error {
	if err := w.thereIsAReleaseBranch(ctx); err != nil {
		return err
	}
	changed, err := w.git(ctx, nil, "diff", "--name-only", "main", w.cfg.Branch, "--", path)
	if err != nil {
		return err
	}
	if changed == "" {
		return fmt.Errorf("the release branch is %w: it doesn't change %s", errNotAsExpected, path)
	}
	content, err := w.git(ctx, nil, "show", w.cfg.Branch+":"+path)
	if err != nil {
		return err
	}
	return compare(path+" on the release branch", expected.Content, w.readable(content))
}

func (w *world) theReleaseCommitMessage(ctx context.Context, expected *godog.DocString) error {
	message, err := w.git(ctx, nil, "log", "-1", "--format=%B", w.cfg.Branch)
	if err != nil {
		return err
	}
	return compare("the release commit message", expected.Content, message)
}

func (w *world) theseTagsAreCreated(ctx context.Context, expected *godog.Table) error {
	created, err := w.createdTags(ctx)
	if err != nil {
		return err
	}
	var want []string
	for _, r := range expected.Rows[1:] {
		want = append(want, r.Cells[0].Value+" on "+r.Cells[1].Value)
	}
	slices.Sort(want)
	return compare("created tags", strings.Join(want, "\n"), strings.Join(created, "\n"))
}

func (w *world) noTagsAreCreated(ctx context.Context) error {
	created, err := w.createdTags(ctx)
	if err != nil {
		return err
	}
	return compare("created tags", "", strings.Join(created, "\n"))
}

func (w *world) theWorkingTreeIsUntouched(ctx context.Context) error {
	now, err := w.snapshot(ctx)
	if err != nil {
		return err
	}
	if now.head != w.before.head {
		return fmt.Errorf("HEAD is %w: it moved from %s to %s",
			errNotAsExpected, w.readable(w.before.head), w.readable(now.head))
	}
	return compare("git status", w.before.status, now.status)
}

func (w *world) nothingChanges(ctx context.Context) error {
	if err := w.theWorkingTreeIsUntouched(ctx); err != nil {
		return err
	}
	now, err := w.snapshot(ctx)
	if err != nil {
		return err
	}
	return compare("branches and tags", w.readable(w.before.refs), w.readable(now.refs))
}

// helpers

func (w *world) thereIsAReleaseBranch(ctx context.Context) error {
	if _, err := w.git(ctx, nil, "rev-parse", "--verify", "--quiet", w.cfg.Branch); err != nil {
		return fmt.Errorf("the release branch is %w: there is no %s, and release-bot said:\n%s",
			errNotAsExpected, w.cfg.Branch, w.output)
	}
	return nil
}

func (w *world) snapshot(ctx context.Context) (snapshot, error) {
	var snap snapshot
	var err error
	if snap.head, err = w.git(ctx, nil, "rev-parse", "HEAD"); err != nil {
		return snap, err
	}
	if snap.status, err = w.git(ctx, nil, "status", "--porcelain"); err != nil {
		return snap, err
	}
	if snap.refs, err = w.git(ctx, nil, "for-each-ref", "--format=%(refname) %(objectname)"); err != nil {
		return snap, err
	}
	tags, err := w.git(ctx, nil, "tag", "--list")
	snap.tags = strings.Fields(tags)
	return snap, err
}

// createdTags lists the tags made by the last command, as "tag on commit", sorted.
func (w *world) createdTags(ctx context.Context) ([]string, error) {
	tags, err := w.git(ctx, nil, "tag", "--list")
	if err != nil {
		return nil, err
	}
	var created []string
	for tag := range strings.FieldsSeq(tags) {
		if slices.Contains(w.before.tags, tag) {
			continue
		}
		commit, err := w.git(ctx, nil, "rev-parse", tag+"^{commit}")
		if err != nil {
			return nil, err
		}
		created = append(created, tag+" on "+w.readable(commit))
	}
	slices.Sort(created)
	return created, nil
}

func (w *world) manifestAt(ctx context.Context, rev string) (manifest.Manifest, error) {
	data, err := w.git(ctx, nil, "show", rev+":"+w.cfg.Manifest)
	if err != nil {
		return manifest.Manifest{}, nil //nolint:nilerr // no manifest yet means nothing released
	}
	return manifest.Parse([]byte(data))
}

func (w *world) label(name, sha string) {
	w.labels[name] = sha
	w.shas[sha] = name
}

func (w *world) labelHead(ctx context.Context, name string) error {
	sha, err := w.git(ctx, nil, "rev-parse", "HEAD")
	if err == nil {
		w.label(name, sha)
	}
	return err
}

// readable replaces commit hashes with the labels the feature gave them.
func (w *world) readable(text string) string {
	for sha, name := range w.shas {
		text = strings.ReplaceAll(text, sha, name)
	}
	for sha, name := range w.shas {
		text = strings.ReplaceAll(text, vcs.Short(sha), name)
	}
	return strings.ReplaceAll(text, w.dir, "<repo>")
}

func (w *world) getenv(name string) string {
	return w.env[name]
}

func (w *world) write(path, content string) error {
	full := filepath.Join(w.dir, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		return err
	}
	return os.WriteFile(full, []byte(content), 0o600)
}

func (w *world) git(ctx context.Context, stdin *strings.Reader, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = w.dir
	if stdin != nil {
		cmd.Stdin = stdin
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

func compare(what, want, got string) error {
	want, got = strings.TrimSpace(want), strings.TrimSpace(got)
	if want == got {
		return nil
	}
	return fmt.Errorf("%s is %w\n--- expected ---\n%s\n--- actual ---\n%s", what, errNotAsExpected, want, got)
}

func row(cells ...string) string {
	return strings.Join(cells, " | ")
}

func orUnreleased(v string) string {
	if v == "" {
		return "unreleased"
	}
	return v
}
