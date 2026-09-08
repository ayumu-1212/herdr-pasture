// Package group turns a herdr snapshot into repository groups of agent rows.
package group

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// RepoInfo describes where a cwd belongs. Root is the grouping key: the main
// repository path for worktrees, the toplevel for ordinary checkouts, and ""
// when cwd is not inside a git repository.
type RepoInfo struct {
	Root       string
	Branch     string
	IsWorktree bool
}

// Resolver maps a working directory to its RepoInfo.
type Resolver interface {
	Resolve(cwd string) RepoInfo
}

type gitRunner func(dir string, args ...string) (string, error)

// GitResolver shells out to git and caches results per cwd for the process
// lifetime. It is safe for concurrent use: Resolve locks around both the
// cache and the underlying git calls, since callers (bubbletea tea.Cmd
// closures backing the poll loop) may invoke it from multiple goroutines at
// once. Resolutions happen at roughly one per second, so holding the lock
// across the git calls themselves is fine.
type GitResolver struct {
	run   gitRunner
	mu    sync.Mutex
	cache map[string]RepoInfo
}

// NewGitResolver returns a resolver backed by the git binary on PATH, with a
// 5s timeout per invocation so a wedged git process can never block the poll
// loop forever.
func NewGitResolver() *GitResolver {
	return newGitResolver(execGitRunner{Bin: "git", Timeout: 5 * time.Second}.run)
}

func newGitResolver(run gitRunner) *GitResolver {
	return &GitResolver{run: run, cache: map[string]RepoInfo{}}
}

// Resolve implements Resolver.
func (g *GitResolver) Resolve(cwd string) RepoInfo {
	g.mu.Lock()
	defer g.mu.Unlock()
	if info, ok := g.cache[cwd]; ok {
		return info
	}
	info, cacheable := g.resolve(cwd)
	if cacheable {
		g.cache[cwd] = info
	}
	return info
}

func (g *GitResolver) resolve(cwd string) (RepoInfo, bool) {
	top, err := g.run(cwd, "rev-parse", "--show-toplevel")
	if err != nil || top == "" {
		// A missing git binary is a host-level problem, not a fact about
		// cwd: don't let it get baked into the cache as a permanent "not a
		// git repo" verdict.
		var execErr *exec.Error
		if errors.As(err, &execErr) {
			return RepoInfo{}, false
		}
		return RepoInfo{}, true
	}
	top = filepath.Clean(top)

	common, err := g.run(cwd, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil || common == "" {
		return RepoInfo{Root: top}, true
	}
	common = filepath.Clean(common)
	if filepath.Base(common) != ".git" {
		// Submodules report a common-dir like "<main>/.git/modules/<name>"
		// and --separate-git-dir checkouts report an arbitrary path;
		// neither has "<root>/.git" shape, so Dir(common) isn't a
		// meaningful grouping key. Treat cwd as its own root instead of
		// folding it into a bogus shared parent or misreporting it as a
		// worktree.
		return RepoInfo{Root: top}, true
	}
	mainRoot := filepath.Dir(common)
	if mainRoot == top {
		return RepoInfo{Root: top}, true
	}
	branch, _ := g.run(cwd, "rev-parse", "--abbrev-ref", "HEAD")
	if branch == "HEAD" {
		// Detached HEAD: "HEAD" isn't a branch name worth showing.
		branch = ""
	}
	return RepoInfo{Root: mainRoot, Branch: branch, IsWorktree: true}, true
}

// execGitRunner shells out to a git binary and implements gitRunner via run.
// It mirrors internal/herdr/runner.ExecRunner: a bounded timeout keeps a
// wedged process from blocking the poll loop forever, and stderr is folded
// into the returned error. Bin is swappable so tests can exercise it against
// a stand-in binary instead of a real git checkout.
type execGitRunner struct {
	// Bin is the executable to invoke; NewGitResolver defaults it to "git".
	Bin string
	// Timeout bounds a single invocation. Zero means no limit.
	Timeout time.Duration
}

func (r execGitRunner) run(dir string, args ...string) (string, error) {
	ctx := context.Background()
	if r.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.Timeout)
		defer cancel()
	}
	argv := append([]string{"-C", dir}, args...)
	cmd := exec.CommandContext(ctx, r.Bin, argv...)
	cmd.WaitDelay = time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("%s %s: %w (timeout %s)", r.Bin, strings.Join(argv, " "), ctx.Err(), r.Timeout)
		}
		msg := fmt.Errorf("%s %s: %w", r.Bin, strings.Join(argv, " "), err)
		if stderrText := strings.TrimSpace(stderr.String()); stderrText != "" {
			msg = fmt.Errorf("%w: %s", msg, stderrText)
		}
		return "", msg
	}
	return strings.TrimSpace(stdout.String()), nil
}
