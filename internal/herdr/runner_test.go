package herdr

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestExecRunnerIncludesStderrAndExitError(t *testing.T) {
	r := ExecRunner{Bin: "sh"}
	_, err := r.Run("-c", "echo boom >&2; exit 3")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error should contain stderr text, got %v", err)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected errors.As to find *exec.ExitError, got %v", err)
	}
}

func TestExecRunnerCapturesStdout(t *testing.T) {
	r := ExecRunner{Bin: "sh"}
	out, err := r.Run("-c", "echo hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(string(out)) != "hi" {
		t.Fatalf("stdout = %q", out)
	}
}

func TestNewExecRunnerUsesEnvOrDefault(t *testing.T) {
	t.Run("custom bin path", func(t *testing.T) {
		t.Setenv("HERDR_BIN_PATH", "/x/herdr")
		if got := NewExecRunner().Bin; got != "/x/herdr" {
			t.Fatalf("Bin = %q, want /x/herdr", got)
		}
	})
	t.Run("default bin", func(t *testing.T) {
		t.Setenv("HERDR_BIN_PATH", "")
		if got := NewExecRunner().Bin; got != "herdr" {
			t.Fatalf("Bin = %q, want herdr", got)
		}
	})
}

func TestExecRunnerTimesOut(t *testing.T) {
	r := ExecRunner{Bin: "sh", Timeout: 50 * time.Millisecond}
	start := time.Now()
	_, err := r.Run("-c", "sleep 5")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("error should mention timeout, got %v", err)
	}
	if elapsed > 900*time.Millisecond {
		t.Fatalf("Run took %s, want well under a second", elapsed)
	}
}
