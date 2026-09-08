// Package group turns a herdr snapshot into repository groups of agent rows.
package group

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
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
// lifetime. It is not safe for concurrent use.
type GitResolver struct {
	run   gitRunner
	cache map[string]RepoInfo
}

// NewGitResolver returns a resolver backed by the git binary on PATH.
func NewGitResolver() *GitResolver {
	return newGitResolver(execGit)
}

func newGitResolver(run gitRunner) *GitResolver {
	return &GitResolver{run: run, cache: map[string]RepoInfo{}}
}

// Resolve implements Resolver.
func (g *GitResolver) Resolve(cwd string) RepoInfo {
	if info, ok := g.cache[cwd]; ok {
		return info
	}
	info := g.resolve(cwd)
	g.cache[cwd] = info
	return info
}

func (g *GitResolver) resolve(cwd string) RepoInfo {
	top, err := g.run(cwd, "rev-parse", "--show-toplevel")
	if err != nil || top == "" {
		return RepoInfo{}
	}
	common, err := g.run(cwd, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil || common == "" {
		return RepoInfo{Root: top}
	}
	mainRoot := filepath.Dir(filepath.Clean(common))
	if mainRoot == filepath.Clean(top) {
		return RepoInfo{Root: top}
	}
	branch, _ := g.run(cwd, "rev-parse", "--abbrev-ref", "HEAD")
	return RepoInfo{Root: mainRoot, Branch: branch, IsWorktree: true}
}

func execGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.String()), nil
}
