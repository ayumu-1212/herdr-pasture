// Package herdr wraps the herdr CLI so callers never build argv themselves.
package herdr

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Runner executes one herdr CLI invocation and returns its stdout.
type Runner interface {
	Run(args ...string) ([]byte, error)
}

// ExecRunner runs the real herdr binary.
type ExecRunner struct {
	Bin string
}

// NewExecRunner uses HERDR_BIN_PATH when set, else "herdr" from PATH.
func NewExecRunner() ExecRunner {
	bin := os.Getenv("HERDR_BIN_PATH")
	if bin == "" {
		bin = "herdr"
	}
	return ExecRunner{Bin: bin}
}

// Run executes herdr with args. On a non-zero exit the error includes stderr,
// which is where herdr prints its JSON error.
func (r ExecRunner) Run(args ...string) ([]byte, error) {
	cmd := exec.Command(r.Bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("herdr %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}
