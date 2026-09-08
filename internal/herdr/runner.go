// Package herdr wraps the herdr CLI so callers never build argv themselves.
package herdr

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Runner executes one herdr CLI invocation and returns its stdout.
type Runner interface {
	Run(args ...string) ([]byte, error)
}

// ExecRunner runs the real herdr binary.
type ExecRunner struct {
	Bin string
	// Timeout bounds a single invocation. Zero means no limit.
	Timeout time.Duration
}

// NewExecRunner uses HERDR_BIN_PATH when set, else "herdr" from PATH.
func NewExecRunner() ExecRunner {
	bin := os.Getenv("HERDR_BIN_PATH")
	if bin == "" {
		bin = "herdr"
	}
	return ExecRunner{Bin: bin, Timeout: 5 * time.Second}
}

// Run executes herdr with args. On a non-zero exit the error includes stderr,
// which is where herdr prints its JSON error. A hung herdr process is killed
// after Timeout, if set, so callers (poll loops, event hooks) never block
// forever.
func (r ExecRunner) Run(args ...string) ([]byte, error) {
	ctx := context.Background()
	if r.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.Timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, r.Bin, args...)
	cmd.WaitDelay = time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("herdr %s: %w (timeout %s)", strings.Join(args, " "), ctx.Err(), r.Timeout)
		}
		msg := fmt.Errorf("herdr %s: %w", strings.Join(args, " "), err)
		if stderrText := strings.TrimSpace(stderr.String()); stderrText != "" {
			msg = fmt.Errorf("%w: %s", msg, stderrText)
		}
		return nil, msg
	}
	return stdout.Bytes(), nil
}
