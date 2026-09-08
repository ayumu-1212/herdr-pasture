package group

import (
	"errors"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

// fakeGit answers `git -C dir <args>` from a table keyed by dir + " " + joined args.
type fakeGit struct {
	answers map[string]string
	calls   int
}

func (f *fakeGit) run(dir string, args ...string) (string, error) {
	f.calls++
	key := dir + " " + strings.Join(args, " ")
	if out, ok := f.answers[key]; ok {
		return out, nil
	}
	return "", errors.New("fatal: not a git repository")
}

func TestResolveMainWorktree(t *testing.T) {
	g := &fakeGit{answers: map[string]string{
		"/repo/sub rev-parse --show-toplevel":                         "/repo",
		"/repo/sub rev-parse --path-format=absolute --git-common-dir": "/repo/.git",
	}}
	r := newGitResolver(g.run)
	got := r.Resolve("/repo/sub")
	want := RepoInfo{Root: "/repo"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestResolveLinkedWorktree(t *testing.T) {
	g := &fakeGit{answers: map[string]string{
		"/repo/.worktrees/feat rev-parse --show-toplevel":                         "/repo/.worktrees/feat",
		"/repo/.worktrees/feat rev-parse --path-format=absolute --git-common-dir": "/repo/.git",
		"/repo/.worktrees/feat rev-parse --abbrev-ref HEAD":                       "feat/x",
	}}
	got := newGitResolver(g.run).Resolve("/repo/.worktrees/feat")
	want := RepoInfo{Root: "/repo", Branch: "feat/x", IsWorktree: true}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestResolveLinkedWorktreeDetachedHeadClearsBranch(t *testing.T) {
	g := &fakeGit{answers: map[string]string{
		"/repo/.worktrees/feat rev-parse --show-toplevel":                         "/repo/.worktrees/feat",
		"/repo/.worktrees/feat rev-parse --path-format=absolute --git-common-dir": "/repo/.git",
		"/repo/.worktrees/feat rev-parse --abbrev-ref HEAD":                       "HEAD",
	}}
	got := newGitResolver(g.run).Resolve("/repo/.worktrees/feat")
	want := RepoInfo{Root: "/repo", Branch: "", IsWorktree: true}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestResolveNonGitReturnsEmpty(t *testing.T) {
	got := newGitResolver((&fakeGit{answers: map[string]string{}}).run).Resolve("/tmp/x")
	if got != (RepoInfo{}) {
		t.Fatalf("got %+v", got)
	}
}

func TestResolveNonGitCachesEmptyResult(t *testing.T) {
	g := &fakeGit{answers: map[string]string{}}
	r := newGitResolver(g.run)
	r.Resolve("/tmp/x")
	after := g.calls
	r.Resolve("/tmp/x")
	if g.calls != after {
		t.Fatalf("expected no additional git calls on cached non-git cwd, got %d more", g.calls-after)
	}
}

func TestResolveCachesPerCwd(t *testing.T) {
	g := &fakeGit{answers: map[string]string{
		"/repo rev-parse --show-toplevel":                         "/repo",
		"/repo rev-parse --path-format=absolute --git-common-dir": "/repo/.git",
	}}
	r := newGitResolver(g.run)
	r.Resolve("/repo")
	r.Resolve("/repo")
	if g.calls != 2 {
		t.Fatalf("expected 2 git calls total (cached second Resolve), got %d", g.calls)
	}
}

func TestResolveCommonDirFailureFallsBackToToplevel(t *testing.T) {
	g := &fakeGit{answers: map[string]string{
		"/repo rev-parse --show-toplevel": "/repo",
		// --git-common-dir deliberately absent from the table: g.run errors.
	}}
	got := newGitResolver(g.run).Resolve("/repo")
	want := RepoInfo{Root: "/repo"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestResolveSubmoduleIsNotFoldedIntoMainRepo(t *testing.T) {
	g := &fakeGit{answers: map[string]string{
		"/main/sub rev-parse --show-toplevel":                         "/main/sub",
		"/main/sub rev-parse --path-format=absolute --git-common-dir": "/main/.git/modules/sub",
	}}
	got := newGitResolver(g.run).Resolve("/main/sub")
	want := RepoInfo{Root: "/main/sub"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestResolveSeparateGitDirIsNotMisreportedAsWorktree(t *testing.T) {
	g := &fakeGit{answers: map[string]string{
		"/repo rev-parse --show-toplevel":                         "/repo",
		"/repo rev-parse --path-format=absolute --git-common-dir": "/elsewhere/repo-gitdir",
	}}
	got := newGitResolver(g.run).Resolve("/repo")
	want := RepoInfo{Root: "/repo"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestResolveDoesNotCacheWhenGitBinaryMissing(t *testing.T) {
	calls := 0
	run := func(dir string, args ...string) (string, error) {
		calls++
		return execGitRunner{Bin: "herdr-pasture-definitely-not-a-real-binary"}.run(dir, args...)
	}
	r := newGitResolver(run)
	r.Resolve("/repo")
	r.Resolve("/repo")
	if calls != 2 {
		t.Fatalf("expected git to be invoked again after a missing-binary error (result must not be cached), got %d calls", calls)
	}
}

func TestResolveConcurrentSameAndDifferentCwds(t *testing.T) {
	g := &fakeGit{answers: map[string]string{
		"/repo rev-parse --show-toplevel":                          "/repo",
		"/repo rev-parse --path-format=absolute --git-common-dir":  "/repo/.git",
		"/other rev-parse --show-toplevel":                         "/other",
		"/other rev-parse --path-format=absolute --git-common-dir": "/other/.git",
	}}
	r := newGitResolver(g.run)
	cwds := []string{"/repo", "/repo", "/other", "/other", "/repo", "/other"}
	var wg sync.WaitGroup
	for _, cwd := range cwds {
		wg.Add(1)
		go func(cwd string) {
			defer wg.Done()
			r.Resolve(cwd)
		}(cwd)
	}
	wg.Wait()

	if got := r.Resolve("/repo"); got != (RepoInfo{Root: "/repo"}) {
		t.Fatalf("got %+v", got)
	}
	if got := r.Resolve("/other"); got != (RepoInfo{Root: "/other"}) {
		t.Fatalf("got %+v", got)
	}
}

func TestExecGitRunnerTrimsOutputAndOrdersArgv(t *testing.T) {
	// "echo" stands in for git here: it prints its argv back followed by a
	// trailing newline, which lets us assert both that -C/dir come first
	// followed by the caller's args (argv order), and that the trailing
	// newline is trimmed from the returned string.
	out, err := (execGitRunner{Bin: "echo"}).run("/some/dir", "rev-parse", "--show-toplevel")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "-C /some/dir rev-parse --show-toplevel"
	if out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}

func TestExecGitRunnerMissingBinaryReturnsExecError(t *testing.T) {
	_, err := (execGitRunner{Bin: "herdr-pasture-definitely-not-a-real-binary"}).run("/some/dir", "rev-parse", "--show-toplevel")
	if err == nil {
		t.Fatal("expected an error")
	}
	var execErr *exec.Error
	if !errors.As(err, &execErr) {
		t.Fatalf("expected errors.As to find *exec.Error, got %T: %v", err, err)
	}
}
