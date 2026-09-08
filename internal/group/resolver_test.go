package group

import (
	"errors"
	"testing"
)

// fakeGit answers `git -C dir <args>` from a table keyed by dir + " " + joined args.
type fakeGit struct {
	answers map[string]string
	calls   int
}

func (f *fakeGit) run(dir string, args ...string) (string, error) {
	f.calls++
	key := dir + " " + joinArgs(args)
	if out, ok := f.answers[key]; ok {
		return out, nil
	}
	return "", errors.New("fatal: not a git repository")
}

func joinArgs(args []string) string {
	s := ""
	for i, a := range args {
		if i > 0 {
			s += " "
		}
		s += a
	}
	return s
}

func TestResolveMainWorktree(t *testing.T) {
	g := &fakeGit{answers: map[string]string{
		"/repo/sub rev-parse --show-toplevel":                        "/repo",
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
		"/repo/.worktrees/feat rev-parse --show-toplevel":                        "/repo/.worktrees/feat",
		"/repo/.worktrees/feat rev-parse --path-format=absolute --git-common-dir": "/repo/.git",
		"/repo/.worktrees/feat rev-parse --abbrev-ref HEAD":                      "feat/x",
	}}
	got := newGitResolver(g.run).Resolve("/repo/.worktrees/feat")
	want := RepoInfo{Root: "/repo", Branch: "feat/x", IsWorktree: true}
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

func TestResolveCachesPerCwd(t *testing.T) {
	g := &fakeGit{answers: map[string]string{
		"/repo rev-parse --show-toplevel":                        "/repo",
		"/repo rev-parse --path-format=absolute --git-common-dir": "/repo/.git",
	}}
	r := newGitResolver(g.run)
	r.Resolve("/repo")
	r.Resolve("/repo")
	if g.calls != 2 {
		t.Fatalf("expected 2 git calls total (cached second Resolve), got %d", g.calls)
	}
}
